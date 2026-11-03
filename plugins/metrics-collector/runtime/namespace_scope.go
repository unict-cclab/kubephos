package metricscollector

import (
	"regexp"
	"strconv"
	"strings"
)

var rateVector = regexp.MustCompile(`\brate\(([^()]*)\[1m\]\)`)
var instantVector = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*\{[^{}]*\}`)

// A reused namespace can still have previous-run samples in a rate lookback.
// Match historical samples to pods created for the current deployment, before
// aggregation, including pods that were evicted later during this same run.
func scopeReusableNamespaceMetrics(definitions []metricDefinition, deployment applicationDeployment) []metricDefinition {
	scope := deployment.Spec.NamespaceCreatedAt
	if scope == nil || scope.IsZero() {
		return definitions
	}
	namespace := strconv.Quote(deployment.Spec.Namespace)
	pods := "(max by (pod) (kube_pod_created{namespace=" + namespace + "} >= " + strconv.FormatInt(scope.Unix(), 10) + "))"
	for i := range definitions {
		id := definitions[i].ID
		if !(strings.HasPrefix(id, "load.") || strings.HasPrefix(id, "application.") || strings.HasPrefix(id, "redis.") || id == "cpu.cores" || id == "memory.working_set" || id == "pod.restarts") {
			continue
		}
		query := definitions[i].Query
		if strings.Contains(query, "rate(") {
			definitions[i].Query = rateVector.ReplaceAllStringFunc(query, func(vector string) string {
				if !strings.Contains(vector, "namespace="+namespace) {
					return vector
				}
				return "(" + vector + " and on (pod) " + pods + ")"
			})
		} else {
			definitions[i].Query = instantVector.ReplaceAllStringFunc(query, func(vector string) string {
				if !strings.Contains(vector, "namespace="+namespace) {
					return vector
				}
				return "(" + vector + " and on (pod) " + pods + ")"
			})
		}
	}
	return definitions
}
