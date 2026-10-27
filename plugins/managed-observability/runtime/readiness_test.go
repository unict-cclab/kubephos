package managedobservability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

type startingRunner struct {
	fakeRunner
	podReads, pvcReads, endpointReads          int
	podFailures, pvcFailures, endpointFailures int
}

func (r *startingRunner) Kubectl(ctx context.Context, config string, input []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	switch {
	case command == "get pods,pvc -n "+namespace+" -o wide":
		return "prometheus-0 0/2 PodInitializing", nil
	case command == "get pods -n "+namespace+" -o json":
		r.podReads++
		if r.podReads <= r.podFailures {
			return `{"items":[{"metadata":{"name":"prometheus-0"},"status":{"phase":"Pending"}}]}`, nil
		}
	case strings.HasPrefix(command, "get pvc -n "+namespace):
		r.pvcReads++
		if r.pvcReads <= r.pvcFailures {
			return "Pending\nBound\n", nil
		}
	case strings.HasPrefix(command, "get --raw=/api/v1/namespaces/"):
		r.endpointReads++
		if r.endpointReads <= r.endpointFailures {
			return "", errors.New("service has no ready endpoints")
		}
	}
	return r.fakeRunner.Kubectl(ctx, config, input, args...)
}

func TestVerifyWaitsForSlowPrometheusStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &startingRunner{podFailures: 3, pvcFailures: 1, endpointFailures: 1}
		plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}}
		plan, err := plugin.Plan(context.Background(), []byte(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`))
		if err != nil {
			t.Fatal(err)
		}
		step := plan.Steps[0]
		step.ResolvedInputs = testArtifacts()
		value, err := plugin.Execute(context.Background(), step, discardLog)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		health, err := plugin.Verify(context.Background(), step, value, discardLog)
		if err != nil || health.Status != domain.HealthHealthy {
			t.Fatalf("slow startup must pass verification: %v %+v", err, health)
		}
		if time.Since(start) < 25*time.Second {
			t.Fatal("verification did not wait through pod, volume and endpoint readiness transitions")
		}
		if runner.podReads < 6 || runner.pvcReads < 3 || runner.endpointReads < 3 {
			t.Fatalf("readiness checks were not retried: %+v", runner)
		}
		if !runner.installed {
			t.Fatal("slow startup must not remove the managed release")
		}
	})
}

func TestReadinessTimeoutRetainsLastFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &startingRunner{podFailures: 1000}
		start := time.Now()
		err := waitForManagedReadiness(context.Background(), runner, "config", nil, 20*time.Second, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "timed out after 20s") || !strings.Contains(err.Error(), "prometheus-0 is not ready") {
			t.Fatalf("missing timeout and pod diagnostic: %v", err)
		}
		if time.Since(start) != 20*time.Second {
			t.Fatalf("readiness exceeded its deadline: %s", time.Since(start))
		}
		if runner.podReads != 4 {
			t.Fatalf("unexpected number of readiness attempts: %d", runner.podReads)
		}
	})
}

func TestReadinessCancellationStopsRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(7*time.Second, cancel)
		runner := &startingRunner{podFailures: 1000}
		err := waitForManagedReadiness(ctx, runner, "config", nil, 5*time.Minute, 5*time.Second)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		if runner.podReads != 2 {
			t.Fatalf("continued retrying after cancellation: %d", runner.podReads)
		}
	})
}

func TestReadinessDeadlineBoundsEachCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := waitForReadiness(context.Background(), 10*time.Second, 5*time.Second, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
		if err == nil || !strings.Contains(err.Error(), "timed out after 10s") {
			t.Fatalf("missing bounded timeout: %v", err)
		}
		if time.Since(start) != 10*time.Second {
			t.Fatal("a blocked readiness request exceeded the deadline")
		}
	})
}

func TestVerifyLogsDiagnosticsOnReadinessTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &startingRunner{podFailures: 1000}
		plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}}
		plan, err := plugin.Plan(context.Background(), []byte(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`))
		if err != nil {
			t.Fatal(err)
		}
		step := plan.Steps[0]
		step.ResolvedInputs = testArtifacts()
		value, err := plugin.Execute(context.Background(), step, discardLog)
		if err != nil {
			t.Fatal(err)
		}
		var messages []string
		health, err := plugin.Verify(context.Background(), step, value, func(level, message string) error { messages = append(messages, message); return nil })
		if err != nil || health.Status != domain.HealthUnhealthy || !strings.Contains(health.Summary, "timed out after 5m0s") || !strings.Contains(health.Summary, "prometheus-0") {
			t.Fatalf("unexpected timeout result: %+v %v", health, err)
		}
		text := strings.Join(messages, "\n")
		if !strings.Contains(text, "Managed observability readiness failed:") || !strings.Contains(text, "Managed observability rollout diagnostic:\nprometheus-0 0/2 PodInitializing") {
			t.Fatalf("timeout did not preserve diagnostics: %s", text)
		}
	})
}
