package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// sharedStartClock marks LoadSession windows taken from the published start
	// instant instead of the moment kubectl returned.
	sharedStartClock = "shared-start"
	// sharedStartFile holds the Unix second of the measured profile's t=0 inside
	// every Locust container; the wrapper idles with zero users until it exists.
	sharedStartFile = "/tmp/kubephos-start"
)

type sharedStart struct {
	// profilingStart is the first traffic instant: call-chain profiling (if any)
	// runs from it to warmupStart.
	profilingStart time.Time
	warmupStart    time.Time
	profileStart   time.Time
	// clockOffset is cluster clock minus KubePhos clock. Start instants are
	// cluster-clock values (what the pods and Prometheus see); KubePhos converts
	// its own waits with this offset.
	clockOffset time.Duration
}

// maxClockSkew bounds the tolerated difference between the KubePhos host clock
// and the cluster clock. Pods must agree with each other within podClockSpread.
const (
	maxClockSkew   = 30 * time.Second
	podClockSpread = 2
)

// clockSamples collects (pod clock - KubePhos clock) offsets from acknowledgements.
type clockSamples struct {
	mu      sync.Mutex
	offsets []float64
}

func (samples *clockSamples) add(podUnix int64, before, after time.Time) {
	middle := float64(before.UnixNano()+after.UnixNano()) / 2 / float64(time.Second)
	samples.mu.Lock()
	samples.offsets = append(samples.offsets, float64(podUnix)-middle)
	samples.mu.Unlock()
}

// offset returns the median offset rounded to whole seconds (pods report whole
// seconds, so smaller offsets are indistinguishable from zero). Pods that
// disagree with each other, or a skew beyond maxClockSkew, invalidate the run.
func (samples *clockSamples) offset() (time.Duration, error) {
	if len(samples.offsets) == 0 {
		return 0, nil
	}
	values := append([]float64(nil), samples.offsets...)
	sort.Float64s(values)
	if values[len(values)-1]-values[0] > podClockSpread {
		return 0, fmt.Errorf("Cluster node clocks disagree by %.0fs; synchronize them (NTP) before measuring.", values[len(values)-1]-values[0])
	}
	median := values[len(values)/2]
	if math.Abs(median) < 1.5 {
		return 0, nil
	}
	offset := time.Duration(math.Round(median)) * time.Second
	if offset > maxClockSkew || offset < -maxClockSkew {
		return 0, fmt.Errorf("Cluster clock differs from the KubePhos host clock by %s (limit %s); synchronize the clocks before measuring.", offset, maxClockSkew)
	}
	return offset, nil
}

// sharedStartLead places the start far enough in the future to deliver it to
// every pod with four parallel kubectl exec calls.
var sharedStartLead = func(workers, shapers int) time.Duration {
	if shapers > 0 {
		return time.Duration(16+2*((shapers+workers+3)/4)) * time.Second
	}
	// The extra seconds absorb a cluster clock running ahead of KubePhos.
	return time.Duration(8+2*((workers+3)/4)) * time.Second
}

// drainWait keeps idle workers alive after the profile; replaceable in tests.
var drainWait = func(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func drainSeconds(spec Spec) int {
	if spec.DrainSeconds == nil {
		return defaultDrainSeconds
	}
	return *spec.DrainSeconds
}

// startClockMode is "shared" for finite sessions, whose start KubePhos publishes
// after every worker is ready, and "local" for interactive ones.
func startClockMode(spec Spec) string {
	if spec.Interactive || len(spec.Steps) == 0 {
		return "local"
	}
	return "shared"
}

// armSharedStart publishes one absolute start to every load worker (and, for a
// phased network profile, to every network shaper) before it happens. Workers
// profile the call chain from profilingStart, send warmup traffic from
// warmupStart = profilingStart + profiling and run the measured profile from
// profileStart = warmupStart + warmup; network phases are aligned to profileStart.
// Only profileStart is published: workers derive the earlier phases from it.
// Slow or failed synchronization invalidates the run.
func armSharedStart(ctx context.Context, runner commandRunner, kubeconfig string, deployment applicationDeployment, profile loadProfile, replicas, profilingSeconds, warmupSeconds, durationSeconds int, network bool, now func() time.Time) (sharedStart, error) {
	var shapers []timelinePod
	if network {
		var err error
		shapers, err = networkShapers(ctx, runner, kubeconfig, deployment.Metadata.OwnershipMarker, durationSeconds)
		if err != nil {
			return sharedStart{}, err
		}
	}
	clocks := &clockSamples{}
	armedAt := now().UTC()
	profilingStart := armedAt.Truncate(time.Second).Add(sharedStartLead(replicas, len(shapers)))
	warmupStart := profilingStart.Add(time.Duration(profilingSeconds) * time.Second)
	profileStart := warmupStart.Add(time.Duration(warmupSeconds) * time.Second)
	syncCtx, cancel := context.WithTimeout(ctx, profilingStart.Sub(armedAt)-500*time.Millisecond)
	defer cancel()
	// Workers must hold the start before their first traffic instant.
	firstUnix := strconv.FormatInt(profilingStart.Unix(), 10)
	profileUnix := strconv.FormatInt(profileStart.Unix(), 10)
	if network {
		command := "now=$(date +%s); delta=$((" + profileUnix + " - now)); if test \"$delta\" -lt 2; then echo 'network clock synchronization is late' >&2; exit 1; fi; printf '%s' " + profileUnix + " > " + networkClockFile + ".tmp; mv " + networkClockFile + ".tmp " + networkClockFile + "; printf '%s\\n' \"$now\""
		if err := parallelTimelinePods(syncCtx, shapers, func(ctx context.Context, pod timelinePod) error {
			before := time.Now()
			value, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", "chaos-mesh", pod.Metadata.Name, "-c", "node-shaper", "--", "/bin/sh", "-ec", command)
			if err != nil {
				return err
			}
			now, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return errors.New("Network node returned an invalid clock.")
			}
			clocks.add(now, before, time.Now())
			return nil
		}); err != nil {
			return sharedStart{}, fmt.Errorf("synchronize network shapers before load: %w", err)
		}
		// The clock ConfigMap records the shared profile start for the network run.
		clock := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": networkClockName(deployment.Metadata.OwnershipMarker), "annotations": map[string]string{applicationOwnershipKey: deployment.Metadata.OwnershipMarker}}, "data": map[string]string{"startUnix": profileUnix}}
		raw, _ := json.Marshal(clock)
		if _, err := runner.Run(syncCtx, kubeconfig, raw, "apply", "-n", deployment.Spec.Namespace, "-f", "-"); err != nil {
			return sharedStart{}, err
		}
	}
	rawPods, err := runner.Run(syncCtx, kubeconfig, nil, "get", "pods", "-n", deployment.Spec.Namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "json")
	if err != nil {
		return sharedStart{}, err
	}
	var workers struct {
		Items []timelinePod `json:"items"`
	}
	if json.Unmarshal([]byte(rawPods), &workers) != nil || len(workers.Items) != replicas {
		return sharedStart{}, errors.New("Load workers are not ready to acknowledge the shared start.")
	}
	command := "now=$(date +%s); if test $((" + firstUnix + " - now)) -lt 1; then echo 'shared load start synchronization is late' >&2; exit 1; fi; printf '%s' " + profileUnix + " > " + sharedStartFile + ".tmp; mv " + sharedStartFile + ".tmp " + sharedStartFile + "; printf '%s %s\\n' \"$(cat " + sharedStartFile + ")\" \"$now\""
	err = parallelTimelinePods(syncCtx, workers.Items, func(ctx context.Context, pod timelinePod) error {
		before := time.Now()
		value, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", deployment.Spec.Namespace, pod.Metadata.Name, "-c", "locust", "--", "/bin/sh", "-ec", command)
		if err != nil {
			return err
		}
		fields := strings.Fields(value)
		if len(fields) != 2 || fields[0] != profileUnix {
			return errors.New("Load worker acknowledged a different start clock.")
		}
		now, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return errors.New("Load worker returned an invalid clock.")
		}
		clocks.add(now, before, time.Now())
		return nil
	})
	if err != nil {
		return sharedStart{}, fmt.Errorf("synchronize load workers before traffic: %w", err)
	}
	// The published instants are interpreted by the pods' clocks, which is also
	// the clock of the Prometheus timestamps, so they are recorded unchanged. A
	// skewed KubePhos host only shifts its own waits, through the offset.
	offset, err := clocks.offset()
	if err != nil {
		return sharedStart{}, err
	}
	if profilingStart.Sub(now().Add(offset)) < 500*time.Millisecond {
		return sharedStart{}, errors.New("Shared start synchronization finished too late; no valid run can be collected.")
	}
	return sharedStart{profilingStart: profilingStart, warmupStart: warmupStart, profileStart: profileStart, clockOffset: offset}, nil
}
