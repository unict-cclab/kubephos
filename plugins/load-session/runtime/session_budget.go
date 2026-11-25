package loadsession

import (
	"fmt"
	"time"
)

const (
	// sessionRuntimeTimeout mirrors spec.runtime.timeout in plugin.yaml: KubePhos
	// kills the plugin process running Execute once it elapses, failing the run.
	sessionRuntimeTimeout = 3 * time.Hour
	// sessionSetupReserve covers the untimed work around the timed phases: worker
	// rollout (up to 10 minutes), the task plan, arming the shared start and the
	// final Locust summary (up to 3 minutes).
	sessionSetupReserve = 15 * time.Minute
)

// maxTimedSessionSeconds is the longest profiling + warmup + profile + drain a
// finite session can run before the runtime timeout kills it.
func maxTimedSessionSeconds() int {
	return int((sessionRuntimeTimeout - sessionSetupReserve) / time.Second)
}

// checkSessionBudget rejects a finite session whose timed phases cannot finish
// within the plugin runtime timeout. Without it a long profile was accepted and
// then killed hours later, losing the whole run.
func checkSessionBudget(spec Spec, durationSeconds, profilingSeconds int) error {
	if spec.Action != "start" || durationSeconds <= 0 {
		return nil
	}
	total := profilingSeconds + spec.WarmupSeconds + durationSeconds + drainSeconds(spec)
	if limit := maxTimedSessionSeconds(); total > limit {
		parts := fmt.Sprintf("warmup %ds + profile %ds + drain %ds", spec.WarmupSeconds, durationSeconds, drainSeconds(spec))
		if profilingSeconds > 0 {
			parts = fmt.Sprintf("call-chain profiling %ds + ", profilingSeconds) + parts
		}
		return fmt.Errorf("the load session lasts %s (%s), longer than the supported %s per run", (time.Duration(total) * time.Second).String(), parts, (time.Duration(limit) * time.Second).String())
	}
	return nil
}
