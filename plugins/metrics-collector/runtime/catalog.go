package metricscollector

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

// metricCatalogEntry documents where a metric of the dataset comes from
// (spec.metrics): one entry per metric id present in the series.
type metricCatalogEntry struct {
	Metric            string `json:"metric"`
	Source            string `json:"source"`
	Vantage           string `json:"vantage"`
	Collector         string `json:"collector"`
	Query             string `json:"query,omitempty"`
	RateWindowSeconds int    `json:"rateWindowSeconds,omitempty"`
	Description       string `json:"description"`
}

func (definition metricDefinition) catalogEntry() metricCatalogEntry {
	return metricCatalogEntry{Metric: definition.ID, Source: definition.Source, Vantage: definition.Vantage, Collector: collectorPrometheus, Query: definition.Query, RateWindowSeconds: definition.RateWindowSeconds, Description: definition.Description}
}

func configuredLoadCatalogEntry() metricCatalogEntry {
	return metricCatalogEntry{Metric: "load.configured_rps", Source: sourceKubePhos, Vantage: vantageConfiguration, Collector: collectorKubePhos,
		Description: "Request rate prescribed by the load profile of the LoadSession, evaluated at each sample from its steps (constant, sinusoidal, exponential); a configuration value, not a measurement."}
}

func sloCatalogEntry(p95 metricDefinition, sloMilliseconds int) metricCatalogEntry {
	return metricCatalogEntry{Metric: "load.slo_violation_percentage", Source: sourceKubePhos, Vantage: vantageClient, Collector: collectorKubePhos, Query: p95.Query, RateWindowSeconds: p95.RateWindowSeconds,
		Description: "Derived from load.p95_response_time (Locust, client side): 100 for each step whose p95 exceeds the " + strconv.Itoa(sloMilliseconds) + " ms objective, otherwise 0; its mean is the share of windows violating the objective."}
}

// buildCatalog keeps the first entry of each metric that has series, sorted by id.
func buildCatalog(entries []metricCatalogEntry, series []metricSeries) []metricCatalogEntry {
	present := map[string]bool{}
	for _, item := range series {
		present[item.Metric] = true
	}
	seen := map[string]bool{}
	catalog := []metricCatalogEntry{}
	for _, entry := range entries {
		if !present[entry.Metric] || seen[entry.Metric] {
			continue
		}
		seen[entry.Metric] = true
		catalog = append(catalog, entry)
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Metric < catalog[j].Metric })
	return catalog
}

// mergeCatalogs combines per-cluster catalogs. Queries that differ between
// clusters (cluster-scoped network probes) are listed per cluster.
func mergeCatalogs(groups [][]metricCatalogEntry, names []string) []metricCatalogEntry {
	type collected struct {
		entry   metricCatalogEntry
		queries []string
		owners  []string
	}
	byMetric := map[string]*collected{}
	order := []string{}
	for index, group := range groups {
		for _, entry := range group {
			current, ok := byMetric[entry.Metric]
			if !ok {
				current = &collected{entry: entry}
				byMetric[entry.Metric] = current
				order = append(order, entry.Metric)
			}
			current.queries = append(current.queries, entry.Query)
			current.owners = append(current.owners, names[index])
		}
	}
	result := make([]metricCatalogEntry, 0, len(order))
	for _, metric := range order {
		current := byMetric[metric]
		differs := false
		for _, query := range current.queries {
			differs = differs || query != current.queries[0]
		}
		if differs {
			lines := make([]string, len(current.queries))
			for i, query := range current.queries {
				lines[i] = current.owners[i] + ": " + query
			}
			current.entry.Query = strings.Join(lines, "\n")
		}
		result = append(result, current.entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Metric < result[j].Metric })
	return result
}

func validateCatalog(catalog []metricCatalogEntry, series []metricSeries) error {
	entries := map[string]bool{}
	for _, entry := range catalog {
		if entries[entry.Metric] || entry.Source == "" || entry.Vantage == "" || entry.Collector == "" || entry.Description == "" || entry.RateWindowSeconds < 0 {
			return errors.New("dataset metric catalog is invalid")
		}
		entries[entry.Metric] = true
	}
	present := map[string]bool{}
	for _, item := range series {
		if !entries[item.Metric] {
			return errors.New("dataset metric catalog does not describe " + item.Metric)
		}
		present[item.Metric] = true
	}
	if len(present) != len(entries) {
		return errors.New("dataset metric catalog describes metrics without series")
	}
	return nil
}
