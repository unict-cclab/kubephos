package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"net/http/httputil"
	"net/url"

	"regexp"

	"sync"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"

	"kubephos.dev/kubephos/internal/domain"

	"kubephos.dev/kubephos/internal/keycloak"

	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"

	"kubephos.dev/kubephos/internal/resourceidentity"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/webui"
)

var strategyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var credentialKindPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,79}$`)
var shortResourceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
var componentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var locustParameterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var plotColorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

type Server struct {
	store          *storage.Store
	registry       *plugins.Registry
	artifacts      *artifacts.Client
	vault          *secrets.Vault
	runtime        plugins.ContainerRunner
	loadPlugin     func([]byte) (plugins.Plugin, error)
	version        string
	transferMu     sync.Mutex
	pluginMu       sync.Mutex
	previewMu      sync.Mutex
	previewFlights map[string]*applicationPreviewFlight
	terminalMu     sync.Mutex
	terminals      map[string]terminalTicket
	terminalActive map[string]int
	terminalSSH    *pluginssh.Client
	identity       *keycloak.Client
	applicationURL string
}

type runtimeConfigurator interface {
	plugins.ContainerRunner
	State(context.Context) (string, string)
	Profile(context.Context) (domain.PluginRuntimeProfile, error)
	ValidateProfile(context.Context, domain.PluginRuntimeProfile) error
	ActivateProfile(context.Context, domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error)
	DeactivateProfile(context.Context) error
}

func NewServer(store *storage.Store, registry *plugins.Registry, artifactStore *artifacts.Client, vault *secrets.Vault, runtime plugins.ContainerRunner, loadPlugin func([]byte) (plugins.Plugin, error), identity *keycloak.Client, applicationURL, version, webDirectory string) (http.Handler, error) {
	server := &Server{store: store, registry: registry, artifacts: artifactStore, vault: vault, runtime: runtime, loadPlugin: loadPlugin, identity: identity, applicationURL: applicationURL, version: version, previewFlights: map[string]*applicationPreviewFlight{}, terminals: map[string]terminalTicket{}, terminalActive: map[string]int{}, terminalSSH: pluginssh.New()}
	webHandler, err := webui.Handler(webDirectory)
	if err != nil {
		return nil, err
	}
	refreshContext, cancelRefresh := context.WithTimeout(context.Background(), 5*time.Minute)
	server.refreshChangedExperimentConfigurations(refreshContext)
	cancelRefresh()

	router := http.NewServeMux()
	router.HandleFunc("GET /health/live", server.live)
	router.HandleFunc("GET /health/ready", server.ready)
	router.HandleFunc("GET /api/v1/auth/status", server.authStatus)
	router.HandleFunc("GET /api/v1/auth/login", server.authLogin)
	router.HandleFunc("GET /api/v1/auth/callback", server.authCallback)
	router.HandleFunc("POST /api/v1/auth/setup", server.authSetup)
	router.HandleFunc("POST /api/v1/auth/logout", server.authLogout)
	router.HandleFunc("GET /api/v1/users", server.listUsers)
	router.HandleFunc("POST /api/v1/users", server.createUser)
	router.HandleFunc("DELETE /api/v1/users/{id}", server.deleteUser)
	router.HandleFunc("PATCH /api/v1/users/{id}", server.updateUserEmail)
	router.HandleFunc("POST /api/v1/users/{id}/password", server.resetUserPassword)
	router.HandleFunc("GET /api/v1/users/{id}/clusters", server.listUserClusters)
	router.HandleFunc("PUT /api/v1/users/{id}/clusters", server.replaceUserClusters)
	router.HandleFunc("GET /api/v1/system", server.system)
	router.HandleFunc("POST /api/v1/administration/state/export", server.exportState)
	router.HandleFunc("POST /api/v1/administration/state/inspect", server.inspectState)
	router.HandleFunc("POST /api/v1/administration/state/import", server.importState)
	router.HandleFunc("GET /api/v1/plugins", server.listPlugins)
	router.HandleFunc("POST /api/v1/plugins/inspect", server.inspectPlugin)
	router.HandleFunc("POST /api/v1/plugins", server.importPlugin)
	router.HandleFunc("GET /api/v1/plugin-imports", server.listPluginImports)
	router.HandleFunc("GET /api/v1/plugin-imports/{id}", server.getPluginImport)
	router.HandleFunc("GET /api/v1/plugin-packages", server.listPluginPackages)
	router.HandleFunc("POST /api/v1/plugin-packages/{sequence}/activate", server.activatePluginPackage)
	router.HandleFunc("POST /api/v1/plugin-packages/{sequence}/deactivate", server.deactivatePluginPackage)
	router.HandleFunc("GET /api/v1/plugin-runtime", server.getPluginRuntime)
	router.HandleFunc("POST /api/v1/plugin-runtime/validate", server.validatePluginRuntime)
	router.HandleFunc("POST /api/v1/plugin-runtime/activate", server.activatePluginRuntime)
	router.HandleFunc("POST /api/v1/plugin-runtime/deactivate", server.deactivatePluginRuntime)
	router.HandleFunc("GET /api/v1/catalog/models", server.listCatalogModels)
	router.HandleFunc("POST /api/v1/catalog/models", server.saveCatalogModel)
	router.HandleFunc("PUT /api/v1/catalog/models/{id}", server.saveCatalogModel)
	router.HandleFunc("DELETE /api/v1/catalog/models/{id}", server.archiveCatalogModel)
	router.HandleFunc("GET /api/v1/catalog/applications", server.listCatalogApplications)
	router.HandleFunc("POST /api/v1/catalog/applications", server.importCatalogApplication)
	router.HandleFunc("GET /api/v1/catalog/applications/{id}/manifest", server.getCatalogApplicationManifest)
	router.HandleFunc("POST /api/v1/catalog/applications/{id}/resolve", server.resolveCatalogApplication)
	router.HandleFunc("DELETE /api/v1/catalog/applications/{id}", server.deleteCatalogApplication)
	router.HandleFunc("GET /api/v1/catalog/strategies", server.listCatalogStrategies)
	router.HandleFunc("POST /api/v1/catalog/strategies", server.importCatalogStrategy)
	router.HandleFunc("PUT /api/v1/catalog/strategies/{id}", server.importCatalogStrategy)
	router.HandleFunc("DELETE /api/v1/catalog/strategies/{id}", server.deleteCatalogStrategy)
	router.HandleFunc("GET /api/v1/credentials", server.listCredentials)
	router.HandleFunc("POST /api/v1/credentials", server.createCredential)
	router.HandleFunc("GET /api/v1/connections", server.listConnections)
	router.HandleFunc("GET /api/v1/connections/{id}", server.getConnectionDetails)
	router.HandleFunc("GET /api/v1/connections/{id}/nodes", server.listProxmoxNodes)
	router.HandleFunc("GET /api/v1/connections/{id}/provisioning-suggestions", server.getProvisioningSuggestions)
	router.HandleFunc("POST /api/v1/connections/{id}/secret", server.revealConnectionSecret)
	router.HandleFunc("POST /api/v1/connections", server.createConnection)
	router.HandleFunc("DELETE /api/v1/connections/{id}", server.deleteConnection)
	router.HandleFunc("GET /api/v1/machine-templates", server.listMachineTemplates)
	router.HandleFunc("POST /api/v1/machine-templates", server.createMachineTemplate)
	router.HandleFunc("GET /api/v1/machine-templates/{id}", server.getMachineTemplate)
	router.HandleFunc("DELETE /api/v1/machine-templates/{id}", server.deleteMachineTemplate)
	router.HandleFunc("GET /api/v1/infrastructure-services", server.listInfrastructureServices)
	router.HandleFunc("POST /api/v1/infrastructure-services", server.createInfrastructureService)
	router.HandleFunc("GET /api/v1/infrastructure-services/{id}", server.getInfrastructureService)
	router.HandleFunc("DELETE /api/v1/infrastructure-services/{id}", server.deleteInfrastructureService)
	router.HandleFunc("GET /api/v1/kubernetes-clusters", server.listKubernetesClusters)
	router.HandleFunc("POST /api/v1/kubernetes-clusters", server.createKubernetesCluster)
	router.HandleFunc("GET /api/v1/kubernetes-clusters/{id}", server.getKubernetesCluster)
	router.HandleFunc("DELETE /api/v1/kubernetes-clusters/{id}", server.deleteKubernetesCluster)
	router.HandleFunc("POST /api/v1/kubernetes-clusters/{id}/recreate", server.recreateKubernetesCluster)
	router.HandleFunc("POST /api/v1/kubernetes-clusters/{id}/reconfigure", server.reconfigureKubernetesCluster)
	router.HandleFunc("GET /api/v1/kubernetes-clusters/{id}/kubeconfig", server.downloadKubeconfig)
	router.HandleFunc("GET /api/v1/kubernetes-clusters/{id}/ssh-key", server.downloadClusterSSHKey)
	router.HandleFunc("POST /api/v1/managed-resources/{id}/access", server.revealManagedResourceAccess)
	router.HandleFunc("GET /api/v1/sharing/users", server.sharingUsers)
	for _, kind := range []string{"experiments", "experiment-configurations"} {
		for _, method := range []string{"GET", "PUT"} {
			resourceKind := kind
			router.HandleFunc(method+" /api/v1/"+kind+"/{id}/sharing", func(w http.ResponseWriter, r *http.Request) {
				r.SetPathValue("resourceKind", resourceKind)
				server.resourceSharing(w, r)
			})
		}
	}
	router.HandleFunc("GET /api/v1/experiment-configurations", server.listExperimentConfigurations)
	router.HandleFunc("POST /api/v1/experiment-configurations", server.createExperimentConfiguration)
	router.HandleFunc("GET /api/v1/experiment-configurations/{id}", server.getExperimentConfiguration)
	router.HandleFunc("PUT /api/v1/experiment-configurations/{id}", server.updateExperimentConfiguration)
	router.HandleFunc("POST /api/v1/experiment-configurations/{id}/clone", server.cloneExperimentConfiguration)
	router.HandleFunc("DELETE /api/v1/experiment-configurations/{id}", server.deleteExperimentConfiguration)
	router.HandleFunc("POST /api/v1/experiments/start", server.createExperimentExecution)
	router.HandleFunc("POST /api/v1/experiment-instances", server.createExperimentExecution)
	router.HandleFunc("POST /api/v1/experiment-suites", server.createExperimentSuite)
	router.HandleFunc("GET /api/v1/infrastructure/resources", server.listInfrastructureResources)
	router.HandleFunc("GET /api/v1/terminal-targets", server.listTerminalTargets)
	router.HandleFunc("POST /api/v1/terminals", server.createTerminal)
	router.HandleFunc("GET /api/v1/terminals/{id}/connect", server.connectTerminal)
	router.HandleFunc("GET /api/v1/audit", server.listAuditEvents)
	router.HandleFunc("GET /api/v1/workspaces", server.listWorkspaces)
	router.HandleFunc("POST /api/v1/workspaces", server.createWorkspace)
	router.HandleFunc("GET /api/v1/workspaces/{id}", server.getWorkspace)
	router.HandleFunc("GET /api/v1/pipelines", server.listPipelines)
	router.HandleFunc("POST /api/v1/pipelines", server.createPipeline)
	router.HandleFunc("GET /api/v1/pipelines/{id}", server.getPipeline)
	router.HandleFunc("POST /api/v1/pipelines/{id}/runs", server.createPipelineRun)
	router.HandleFunc("GET /api/v1/pipeline-runs", server.listPipelineRuns)
	router.HandleFunc("GET /api/v1/pipeline-runs/{id}", server.getPipelineRun)
	router.HandleFunc("GET /api/v1/pipeline-runs/{id}/logs", server.listPipelineRunLogs)
	router.HandleFunc("GET /api/v1/pipeline-runs/{id}/strategy-logs", server.listPipelineRunStrategyLogs)
	router.HandleFunc("POST /api/v1/pipeline-runs/{id}/cancel", server.cancelPipelineRun)
	router.HandleFunc("GET /api/v1/experiments", server.listExperiments)
	router.HandleFunc("POST /api/v1/experiments", server.createCompletedExperiment)
	router.HandleFunc("GET /api/v1/experiments/{id}/configuration-snapshot", server.getExperimentConfigurationSnapshot)
	router.HandleFunc("POST /api/v1/experiments/{id}/cancel", server.cancelExperiment)
	router.HandleFunc("DELETE /api/v1/experiments/{id}", server.deleteExperiment)
	router.HandleFunc("GET /api/v1/experiments/{id}/figures", server.listExperimentFigures)
	router.HandleFunc("POST /api/v1/experiments/{id}/figures", server.saveExperimentFigure)
	router.HandleFunc("GET /api/v1/figures/{id}/download", server.downloadExperimentFigure)
	router.HandleFunc("POST /api/v1/experiment-runs", server.createPipelineExperiment)
	router.HandleFunc("GET /api/v1/operations", server.listOperations)
	router.HandleFunc("POST /api/v1/operations", server.createOperation)
	router.HandleFunc("GET /api/v1/operations/{id}", server.getOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/queue", server.queueOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/cancel", server.cancelOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/cleanup", server.createCleanupOperation)
	router.HandleFunc("GET /api/v1/operations/{id}/logs", server.listLogs)
	router.HandleFunc("GET /api/v1/operations/{id}/events", server.operationEvents)
	router.HandleFunc("GET /api/v1/artifacts", server.listAvailableArtifacts)
	router.HandleFunc("GET /api/v1/artifacts/{id}/download", server.downloadArtifact)
	if identity != nil {
		target, parseErr := url.Parse(identity.ProxyTarget())
		if parseErr != nil {
			return nil, parseErr
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(request *http.Request) {
			host := request.Host
			protocol := "http"
			if request.TLS != nil {
				protocol = "https"
			}
			director(request)
			request.Header.Set("X-Forwarded-Host", host)
			request.Header.Set("X-Forwarded-Proto", protocol)
		}
		router.Handle("/auth/", proxy)
	}
	router.Handle("/", webHandler)
	go server.warmApplicationPreviews()
	return securityHeaders(requestLog(server.authenticate(auditMutations(store, attributeResources(store, recoverer(router)))))), nil
}

func (s *Server) live(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) ready(response http.ResponseWriter, request *http.Request) {
	if err := s.store.Ready(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	if err := s.artifacts.Health(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	if state, message := s.runtimeHealth(request.Context()); state == "unavailable" {
		writeError(response, http.StatusServiceUnavailable, "not_ready", "OCI plugin runtime is unavailable: "+message)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) system(response http.ResponseWriter, request *http.Request) {
	stats, err := s.store.Stats(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read platform status.")
		return
	}
	runtimeContext, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	runtimeState, runtimeMessage := s.runtimeHealth(runtimeContext)
	cancel()
	status := "healthy"
	if runtimeState == "unavailable" {
		status = "degraded"
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"name":    "KubePhos",
		"version": s.version,
		"status":  status,
		"features": map[string]bool{
			"ociPluginImport": runtimeState == "healthy",
		},
		"pluginRuntime": map[string]string{"status": runtimeState, "message": runtimeMessage},
		"stats":         stats,
		"checkedAt":     time.Now().UTC(),
	})
}

func (s *Server) runtimeHealth(ctx context.Context) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.runtime == nil {
		return "disabled", "External OCI plugins are not enabled."
	}
	if stateful, ok := s.runtime.(plugins.RuntimeState); ok {
		return stateful.State(ctx)
	}
	if err := s.runtime.Ready(ctx); err != nil {
		return "unavailable", err.Error()
	}
	return "healthy", "The dedicated OCI executor is ready."
}

func resolvedPlanHash(manifest plugins.Manifest, spec json.RawMessage, plan domain.Plan) (string, error) {
	value, err := json.Marshal(struct {
		PluginID      string          `json:"pluginId"`
		PluginVersion string          `json:"pluginVersion"`
		PluginDigest  string          `json:"pluginDigest,omitempty"`
		Spec          json.RawMessage `json:"spec"`
		Plan          domain.Plan     `json:"plan"`
	}{manifest.ID, manifest.Version, manifest.Runtime.Digest, spec, plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

func validatePlanEffects(manifest plugins.Manifest, plan domain.Plan) error {
	if plan.PluginID != manifest.ID {
		return fmt.Errorf("plan plugin identity does not match the selected plugin")
	}
	if len(plan.Steps) > 100 {
		return fmt.Errorf("plan exceeds the maximum number of steps")
	}
	stepIDs := map[string]bool{}
	createdResources := map[string]bool{}
	for _, step := range plan.Steps {
		if step.ID == "" || step.Name == "" || stepIDs[step.ID] || !json.Valid(step.Input) {
			return fmt.Errorf("plan contains an invalid or duplicate step identity")
		}
		stepIDs[step.ID] = true
		for _, effect := range step.Effects {
			if manifest.Provider == "" {
				return fmt.Errorf("resource effects require a provider identity")
			}
			if effect.Action != "create" && effect.Action != "delete" && effect.Action != "update" {
				return fmt.Errorf("step %q declares unsupported effect %q", step.ID, effect.Action)
			}
			if effect.Action == "update" && !manifest.HasCapability("infrastructure.reconfigure") {
				return fmt.Errorf("resource updates require infrastructure.reconfigure")
			}
			if effect.Action == "create" && !manifest.HasCapability("infrastructure.provision") {
				return fmt.Errorf("plugin is not allowed to provision infrastructure")
			}
			if effect.Action == "delete" && !manifest.HasCapability("infrastructure.deprovision") {
				return fmt.Errorf("plugin is not allowed to deprovision infrastructure")
			}
			if effect.ExternalID == "" || effect.Kind == "" || effect.Name == "" {
				return fmt.Errorf("step %q declares an incomplete resource effect", step.ID)
			}
			if effect.Action == "create" {
				ledgerID, err := resourceidentity.LedgerID(manifest.Provider, step.Input, effect.ExternalID)
				if err != nil {
					return err
				}
				if createdResources[ledgerID] {
					return fmt.Errorf("resource %q is created more than once in the plan", effect.ExternalID)
				}
				createdResources[ledgerID] = true
			}
		}
	}
	return nil
}

func (s *Server) validateResourceEffects(ctx context.Context, workspaceID string, manifest plugins.Manifest, plan domain.Plan) error {
	for position, step := range plan.Steps {
		for _, effect := range step.Effects {
			ledgerID, err := resourceidentity.LedgerID(manifest.Provider, step.Input, effect.ExternalID)
			if err != nil {
				return err
			}
			switch effect.Action {
			case "create":
				if err := s.store.EnsureResourceAvailable(ctx, manifest.Provider, ledgerID); err != nil {
					return err
				}
			case "delete", "update":
				createdInPlan := false
				for _, previous := range plan.Steps[:position] {
					for _, candidate := range previous.Effects {
						if candidate.Action == "create" && sameLedgerResource(manifest.Provider, previous.Input, candidate.ExternalID, ledgerID) {
							createdInPlan = true
						}
					}
				}
				if !createdInPlan {
					authorize := s.store.AuthorizeManagedResource
					if step.Cleanup {
						authorize = s.store.AuthorizeManagedCleanup
					}
					if err := authorize(ctx, manifest.Provider, ledgerID, workspaceID); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func terminal(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}

func Serve(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	result := make(chan error, 1)
	go func() {
		result <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func sameLedgerResource(provider string, input json.RawMessage, externalID, expected string) bool {
	actual, err := resourceidentity.LedgerID(provider, input, externalID)
	return err == nil && actual == expected
}
