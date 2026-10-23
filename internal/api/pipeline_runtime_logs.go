package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	kubeconfigutil "kubephos.dev/kubephos/internal/kubeconfig"
	"net/http"
	"net/url"

	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

const autoscalerOwnerLabel = "v1.custompodautoscaler.com/owned-by"

type pipelineStrategyLog struct {
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Namespace string   `json:"namespace,omitempty"`
	Resource  string   `json:"resource,omitempty"`
	State     string   `json:"state"`
	Lines     []string `json:"lines"`
	Message   string   `json:"message,omitempty"`
}

type strategyLogTarget struct {
	pipelineStrategyLog
	Args      []string
	LokiQuery string
}

func (s *Server) listPipelineRunStrategyLogs(response http.ResponseWriter, request *http.Request) {
	run, err := s.store.GetPipelineRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && !s.canReadResource(request, "pipeline-runs", run.ID) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline run not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline run.")
		return
	}
	stages := strategyStages(run.Stages)
	if len(stages) == 0 {
		writeJSON(response, http.StatusOK, map[string]any{"items": []pipelineStrategyLog{}})
		return
	}
	artifacts, err := s.store.ListPipelineRunArtifacts(request.Context(), run.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read runtime log targets.")
		return
	}
	artifactsByOperation := map[string][]domain.Artifact{}
	for _, artifact := range artifacts {
		artifactsByOperation[artifact.OperationID] = append(artifactsByOperation[artifact.OperationID], artifact)
	}
	targets := make([]strategyLogTarget, 0, len(stages))
	for _, stage := range stages {
		target, found := s.strategyLogTarget(request.Context(), stage, artifactsByOperation[stage.OperationID])
		if found {
			targets = append(targets, target)
			continue
		}
		kind, title := strategyStageIdentity(stage)
		state := "waiting"
		message := "Runtime logs will appear after the component is deployed."
		if stage.Status == domain.OperationFailed || stage.Status == domain.OperationCanceled {
			state = "unavailable"
			message = "The component was not deployed successfully."
		}
		targets = append(targets, strategyLogTarget{pipelineStrategyLog: pipelineStrategyLog{Kind: kind, Title: title, State: state, Lines: []string{}, Message: message}})
	}

	kubeconfig, kubeconfigErr := s.pipelineRunKubeconfig(request.Context(), stages)
	start := run.CreatedAt.Add(-30 * time.Second)
	if run.StartedAt != nil {
		start = run.StartedAt.Add(-30 * time.Second)
	}
	end := time.Now().UTC()
	if run.CompletedAt != nil {
		end = run.CompletedAt.Add(30 * time.Second)
	}
	items := collectStrategyLogs(request.Context(), targets, kubeconfig, kubeconfigErr, start, end, runKubectlWithKubeconfig, readLokiRuntimeLogs)

	response.Header().Set("Cache-Control", "no-store, max-age=0")
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

// Collect independently so an unavailable strategy does not delay the others.
// Keep concurrency bounded and preserve the order of the selected strategies.
func collectStrategyLogs(ctx context.Context, targets []strategyLogTarget, kubeconfig string, connectionErr error, start, end time.Time,
	run func(context.Context, string, ...string) (string, error),
	history func(context.Context, string, string, time.Time, time.Time) ([]string, error),
) []pipelineStrategyLog {
	items := make([]pipelineStrategyLog, len(targets))
	slots := make(chan struct{}, 3)
	var workers sync.WaitGroup
	for index, target := range targets {
		items[index] = target.pipelineStrategyLog
		if len(target.Args) == 0 {
			continue
		}
		if connectionErr != nil {
			items[index].State = "unavailable"
			items[index].Message = "The verified cluster connection is unavailable."
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			item := &items[index]
			item.State = "unavailable"
			item.Message = "Runtime logs are not available."
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			liveContext, cancel := context.WithTimeout(ctx, 8*time.Second)
			output, err := run(liveContext, kubeconfig, append(target.Args, "--pod-running-timeout=2s")...)
			cancel()
			if err == nil {
				item.State, item.Lines, item.Message = "available", nonEmptyLines(output), ""
				if len(item.Lines) == 0 {
					item.Message = "No log entries yet."
				}
				return
			}
			if ctx.Err() != nil || target.LokiQuery == "" {
				return
			}
			historyContext, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			lines, err := history(historyContext, kubeconfig, target.LokiQuery, start, end)
			if err == nil && len(lines) > 0 {
				item.State, item.Lines, item.Message = "available", lines, ""
			}
		}()
	}
	workers.Wait()
	return items
}

func strategyStages(stages []domain.PipelineRunStage) []domain.PipelineRunStage {
	result := []domain.PipelineRunStage{}
	for _, stage := range stages {
		kind, _ := strategyStageIdentity(stage)
		if kind != "" {
			result = append(result, stage)
		}
	}
	return result
}

func strategyStageIdentity(stage domain.PipelineRunStage) (string, string) {
	switch stage.PluginID {
	case "io.kubephos.scheduler.kubernetes.secondary":
		return "scheduler", "Custom scheduler"
	case "io.kubephos.descheduler.kubernetes.managed":
		return "descheduler", "Descheduler"
	case "io.kubephos.autoscaler.kubernetes.cpa":
		return "autoscaler", "Autoscaler"
	default:
		return "", ""
	}
}

func (s *Server) strategyLogTarget(ctx context.Context, stage domain.PipelineRunStage, artifacts []domain.Artifact) (strategyLogTarget, bool) {
	kind, title := strategyStageIdentity(stage)
	wantedType := "StrategyDeployment"
	if kind == "scheduler" {
		wantedType = "SchedulerDeployment"
	}
	for _, artifact := range artifacts {
		if artifact.Type != wantedType {
			continue
		}
		raw, err := s.readArtifactPayload(ctx, artifact)
		if err != nil {
			continue
		}
		target, err := strategyLogTargetFromArtifact(kind, title, raw)
		if err == nil {
			return target, true
		}
	}
	return strategyLogTarget{}, false
}

func strategyLogTargetFromArtifact(kind, title string, raw []byte) (strategyLogTarget, error) {
	if kind == "scheduler" {
		var artifact struct {
			Kind string `json:"kind"`
			Spec struct {
				Namespace string `json:"namespace"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &artifact) != nil || artifact.Kind != "SchedulerDeployment" || artifact.Spec.Namespace == "" {
			return strategyLogTarget{}, errors.New("invalid scheduler deployment artifact")
		}
		return strategyLogTarget{
			pipelineStrategyLog: pipelineStrategyLog{Kind: kind, Title: title, Namespace: artifact.Spec.Namespace, Resource: "deployment/scheduler", State: "waiting", Lines: []string{}},
			Args:                []string{"logs", "deployment/scheduler", "-n", artifact.Spec.Namespace, "-c", "scheduler", "--tail=200", "--timestamps=true"},
			LokiQuery:           `{namespace=` + strconv.Quote(artifact.Spec.Namespace) + `,container="scheduler"}`,
		}, nil
	}
	var artifact struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			StrategyKind string   `json:"strategyKind"`
			Namespace    string   `json:"namespace"`
			Targets      []string `json:"targets"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &artifact) != nil || artifact.Kind != "StrategyDeployment" || artifact.Spec.StrategyKind != kind || artifact.Metadata.Name == "" {
		return strategyLogTarget{}, errors.New("invalid strategy deployment artifact")
	}
	if kind == "descheduler" {
		if artifact.Spec.Namespace == "" {
			return strategyLogTarget{}, errors.New("invalid " + kind + " deployment namespace")
		}
		return strategyLogTarget{
			pipelineStrategyLog: pipelineStrategyLog{Kind: kind, Title: title, Namespace: artifact.Spec.Namespace, Resource: "deployment/" + kind, State: "waiting", Lines: []string{}},
			Args:                []string{"logs", "deployment/" + kind, "-n", artifact.Spec.Namespace, "-c", kind, "--tail=200", "--timestamps=true"},
			LokiQuery:           `{namespace=` + strconv.Quote(artifact.Spec.Namespace) + `,container=` + strconv.Quote(kind) + `}`,
		}, nil
	}
	if kind != "autoscaler" || artifact.Spec.Namespace == "" || len(artifact.Spec.Targets) == 0 {
		return strategyLogTarget{}, errors.New("invalid autoscaler deployment artifact")
	}
	owners := make([]string, 0, len(artifact.Spec.Targets))
	for _, target := range artifact.Spec.Targets {
		owners = append(owners, strategyRuntimeResourceName(artifact.Metadata.Name, target))
	}
	sort.Strings(owners)
	selector := autoscalerOwnerLabel + " in (" + strings.Join(owners, ",") + ")"
	podNames := make([]string, len(owners))
	for index, owner := range owners {
		podNames[index] = regexp.QuoteMeta(owner)
	}
	return strategyLogTarget{
		pipelineStrategyLog: pipelineStrategyLog{Kind: kind, Title: title, Namespace: artifact.Spec.Namespace, Resource: strings.Join(owners, ", "), State: "waiting", Lines: []string{}},
		Args:                []string{"logs", "-n", artifact.Spec.Namespace, "-l", selector, "-c", "custom-pod-autoscaler", "--tail=200", "--timestamps=true", "--prefix=true"},
		LokiQuery:           `{namespace=` + strconv.Quote(artifact.Spec.Namespace) + `,container="custom-pod-autoscaler",pod=~` + strconv.Quote("^("+strings.Join(podNames, "|")+")$") + `}`,
	}, nil
}

func (s *Server) pipelineRunKubeconfig(ctx context.Context, stages []domain.PipelineRunStage) (string, error) {
	for _, stage := range stages {
		var spec struct {
			ClusterConnectionRef string `json:"clusterConnectionRef"`
		}
		if json.Unmarshal(stage.Spec, &spec) != nil || spec.ClusterConnectionRef == "" {
			continue
		}
		artifact, err := s.store.GetArtifact(ctx, spec.ClusterConnectionRef)
		if err != nil {
			continue
		}
		raw, err := s.readArtifactPayload(ctx, artifact)
		if err != nil {
			continue
		}
		if kubeconfig := clusterConnectionKubeconfig(raw); kubeconfig != "" {
			return kubeconfig, nil
		}
	}
	return "", errors.New("cluster connection artifact not found")
}

func runKubectlWithKubeconfig(ctx context.Context, kubeconfig string, args ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := kubeconfigutil.RunKubectl(commandContext, kubeconfig, nil, append([]string{"--request-timeout=7s"}, args...)...)
	return string(output), err
}

func readLokiRuntimeLogs(ctx context.Context, kubeconfig, query string, start, end time.Time) ([]string, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("start", start.UTC().Format(time.RFC3339Nano))
	values.Set("end", end.UTC().Format(time.RFC3339Nano))
	values.Set("limit", "200")
	values.Set("direction", "BACKWARD")
	path := "/api/v1/namespaces/kubephos-observability/services/http:kubephos-loki-gateway:80/proxy/loki/api/v1/query_range?" + values.Encode()
	raw, err := runKubectlWithKubeconfig(ctx, kubeconfig, "get", "--raw="+path)
	if err != nil {
		return nil, err
	}
	return decodeLokiRuntimeLogs([]byte(raw))
}

func decodeLokiRuntimeLogs(raw []byte) ([]string, error) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Status != "success" {
		return nil, errors.New("Loki query failed")
	}
	type entry struct {
		timestamp int64
		line      string
	}
	entries := []entry{}
	for _, stream := range response.Data.Result {
		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			line := strings.TrimSpace(value[1])
			if line == "" || strings.HasPrefix(line, "unable to retrieve container logs for containerd://") || strings.HasPrefix(line, "failed to try resolving symlinks in path ") {
				continue
			}
			timestamp, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil {
				continue
			}
			if pod := stream.Stream["pod"]; pod != "" {
				line = "[" + pod + "] " + line
			}
			entries = append(entries, entry{timestamp: timestamp, line: line})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].timestamp < entries[j].timestamp })
	if len(entries) > 200 {
		entries = entries[len(entries)-200:]
	}
	lines := make([]string, len(entries))
	for index, item := range entries {
		lines[index] = item.line
	}
	return lines, nil
}

func strategyRuntimeResourceName(prefix, target string) string {
	value := prefix + "-" + target
	if len(value) > 63 {
		digest := sha256.Sum256([]byte(value))
		value = strings.TrimRight(value[:54], "-") + "-" + hex.EncodeToString(digest[:4])
	}
	return strings.TrimRight(value, "-")
}

func nonEmptyLines(value string) []string {
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
