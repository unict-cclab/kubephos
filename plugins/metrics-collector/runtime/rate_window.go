package metricscollector

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Metric queries are written with this placeholder as their range and resolved
// by applyRateWindow once the run's rate window is known.
const (
	rateWindowToken = "__RATE_WINDOW__"
	rateWindowRange = "[" + rateWindowToken + "]"
	// Legacy window, also used when the scrape interval cannot be detected.
	defaultRateWindow = time.Minute
)

func rateWindowChoices() map[string]time.Duration {
	return map[string]time.Duration{"30s": 30 * time.Second, "1m": time.Minute, "2m": 2 * time.Minute, "5m": 5 * time.Minute}
}

func validRateWindow(value string) bool {
	if value == "" || value == "auto" {
		return true
	}
	_, ok := rateWindowChoices()[value]
	return ok
}

// selectRateWindow chooses the range of rate(), histogram_quantile() and
// avg_over_time() queries.
//
// A rate needs at least two scrapes inside its range, and Prometheus advises
// four to tolerate a missed scrape, hence 4 x scrape interval. It must also be
// at least the step: a shorter range skips the samples between steps. It
// should not be much wider than the step either, because each sample of a
// query_range then averages over the range: adjacent samples overlap, are
// correlated and smooth out bursts, which understates the variability that
// confidence intervals and percentiles of the time series rely on. auto
// therefore uses max(step, 4 x scrape interval), and the legacy 1m when the
// scrape interval is unknown. Explicit values are honoured unless they cover
// fewer than two scrapes, which would leave required series empty; they are
// then widened to 2 x scrape interval.
func selectRateWindow(config string, resolution, scrapeInterval time.Duration) time.Duration {
	if explicit, ok := rateWindowChoices()[config]; ok {
		if scrapeInterval > 0 && explicit < 2*scrapeInterval {
			return time.Duration(math.Ceil((2 * scrapeInterval).Seconds())) * time.Second
		}
		return explicit
	}
	if scrapeInterval <= 0 {
		return defaultRateWindow
	}
	window := 4 * scrapeInterval
	if resolution > window {
		window = resolution
	}
	return time.Duration(math.Ceil(window.Seconds())) * time.Second
}

// formatPromDuration renders whole seconds as a PromQL duration, keeping the
// legacy "1m" spelling for minute multiples.
func formatPromDuration(window time.Duration) string {
	seconds := int64(math.Ceil(window.Seconds()))
	if seconds > 0 && seconds%60 == 0 {
		return strconv.FormatInt(seconds/60, 10) + "m"
	}
	return strconv.FormatInt(seconds, 10) + "s"
}

// applyRateWindow returns copies of the definitions with the range placeholder
// resolved and the window recorded on every definition that uses it.
func applyRateWindow(definitions []metricDefinition, window time.Duration) []metricDefinition {
	result := make([]metricDefinition, len(definitions))
	for i, definition := range definitions {
		if strings.Contains(definition.Query, rateWindowToken) {
			definition.Query = strings.ReplaceAll(definition.Query, rateWindowToken, formatPromDuration(window))
			definition.RateWindowSeconds = int(math.Ceil(window.Seconds()))
		}
		result[i] = definition
	}
	return result
}

// loadScrapeJob is the scrape job of the managed observability stack that
// collects the Locust wrapper and Istio proxy metrics (pod annotation
// discovery on /stats/prometheus).
const loadScrapeJob = "istio-mesh"

// detectScrapeInterval reads the scrape interval of the job that scrapes the
// application namespace (Locust and Istio metrics), first from the active
// targets and then from the loaded configuration. It is best effort: zero
// means unknown.
func (plugin Plugin) detectScrapeInterval(ctx context.Context, cluster clusterConnection, capability observabilityCapability, endpoint serviceEndpoint, namespace string) (time.Duration, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	job := ""
	if raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+proxyBase(capability, endpoint)+"/api/v1/targets?state=active"); err == nil {
		var response struct {
			Status string `json:"status"`
			Data   struct {
				ActiveTargets []struct {
					ScrapePool     string            `json:"scrapePool"`
					ScrapeURL      string            `json:"scrapeUrl"`
					ScrapeInterval string            `json:"scrapeInterval"`
					Labels         map[string]string `json:"labels"`
				} `json:"activeTargets"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &response) == nil && response.Status == "success" {
			best, bestRank := -1, 0
			for i, target := range response.Data.ActiveTargets {
				rank := 0
				switch {
				case target.Labels["namespace"] == namespace && strings.HasPrefix(target.Labels["pod"], "kubephos-load-"):
					rank = 4
				case target.Labels["namespace"] == namespace && strings.Contains(target.ScrapeURL, "/stats/prometheus"):
					rank = 3
				case target.ScrapePool == loadScrapeJob || target.Labels["job"] == loadScrapeJob:
					rank = 2
				}
				if rank > bestRank {
					best, bestRank = i, rank
				}
			}
			if best >= 0 {
				target := response.Data.ActiveTargets[best]
				job = target.Labels["job"]
				if job == "" {
					job = target.ScrapePool
				}
				if interval, err := parsePromDuration(target.ScrapeInterval); err == nil && interval > 0 {
					return interval, job
				}
			}
		}
	}
	if job == "" {
		job = loadScrapeJob
	}
	raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+proxyBase(capability, endpoint)+"/api/v1/status/config")
	if err != nil {
		return 0, ""
	}
	return scrapeIntervalFromConfig(raw, job)
}

func scrapeIntervalFromConfig(raw, job string) (time.Duration, string) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			YAML string `json:"yaml"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &response) != nil || response.Status != "success" {
		return 0, ""
	}
	var config struct {
		Global struct {
			ScrapeInterval string `yaml:"scrape_interval"`
		} `yaml:"global"`
		ScrapeConfigs []struct {
			JobName        string `yaml:"job_name"`
			ScrapeInterval string `yaml:"scrape_interval"`
		} `yaml:"scrape_configs"`
	}
	if yaml.Unmarshal([]byte(response.Data.YAML), &config) != nil {
		return 0, ""
	}
	for _, scrape := range config.ScrapeConfigs {
		if scrape.JobName == job && scrape.ScrapeInterval != "" {
			if interval, err := parsePromDuration(scrape.ScrapeInterval); err == nil && interval > 0 {
				return interval, job
			}
		}
	}
	// Prometheus' own default when the global value is absent is 1m.
	global := config.Global.ScrapeInterval
	if global == "" {
		global = "1m"
	}
	if interval, err := parsePromDuration(global); err == nil && interval > 0 {
		return interval, "global"
	}
	return 0, ""
}

// parsePromDuration accepts the Prometheus duration subset used for scrape
// intervals (ms, s, m, h, d and combinations such as 1m30s).
func parsePromDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(value)
}
