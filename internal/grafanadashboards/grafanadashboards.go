// Package grafanadashboards keeps the KubePhos dashboards and installs them in a
// cluster's managed Grafana through its HTTP API. The API path works for every
// cluster, including those created before a dashboard existed, and updates a
// dashboard only when its embedded definition changes.
package grafanadashboards

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// ExperimentUID identifies the live experiment dashboard in every Grafana.
	ExperimentUID = "kubephos-experiment"
	folderUID     = "kubephos"
	folderTitle   = "KubePhos"
	revisionTag   = "kubephos-revision:"
)

//go:embed kubephos-experiment.json
var experimentDefinition []byte

// ExperimentDefinition returns a copy of the embedded dashboard model.
func ExperimentDefinition() []byte {
	return slices.Clone(experimentDefinition)
}

// revision changes whenever the embedded definition does.
func revision(definition []byte) string {
	sum := sha256.Sum256(definition)
	return revisionTag + hex.EncodeToString(sum[:6])
}

// Grafana addresses one managed Grafana with its administrator identity.
type Grafana struct {
	BaseURL  string
	Username string
	Password string
	Client   *http.Client
}

// EnsureExperiment installs or updates the live experiment dashboard in the
// KubePhos folder. An up-to-date dashboard is left untouched, so repeated calls
// add no versions to Grafana's history.
func (grafana Grafana) EnsureExperiment(ctx context.Context) error {
	current := revision(experimentDefinition)
	var existing struct {
		Dashboard struct {
			Tags []string `json:"tags"`
		} `json:"dashboard"`
		Meta struct {
			Provisioned bool `json:"provisioned"`
		} `json:"meta"`
	}
	status, err := grafana.call(ctx, http.MethodGet, "/api/dashboards/uid/"+ExperimentUID, nil, &existing)
	if err != nil {
		return err
	}
	// A dashboard provisioned from files is read-only through the API; it is
	// already present, so it is used as it is.
	if status == http.StatusOK && (existing.Meta.Provisioned || slices.Contains(existing.Dashboard.Tags, current)) {
		return nil
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("Grafana answered %d while reading the live dashboard", status)
	}
	// An existing folder answers with a conflict, which is the expected outcome.
	status, err = grafana.call(ctx, http.MethodPost, "/api/folders", map[string]string{"uid": folderUID, "title": folderTitle}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusConflict && status != http.StatusPreconditionFailed {
		return fmt.Errorf("Grafana answered %d while preparing the %s folder", status, folderTitle)
	}
	var dashboard map[string]any
	if err := json.Unmarshal(experimentDefinition, &dashboard); err != nil {
		return fmt.Errorf("decode the live dashboard: %w", err)
	}
	tags, _ := dashboard["tags"].([]any)
	dashboard["tags"] = append(tags, current)
	dashboard["id"] = nil
	body := map[string]any{"dashboard": dashboard, "folderUid": folderUID, "overwrite": true, "message": "Updated by KubePhos"}
	status, err = grafana.call(ctx, http.MethodPost, "/api/dashboards/db", body, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("Grafana answered %d while saving the live dashboard", status)
	}
	return nil
}

func (grafana Grafana) call(ctx context.Context, method, path string, payload, result any) (int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(grafana.BaseURL, "/")+path, body)
	if err != nil {
		return 0, err
	}
	request.SetBasicAuth(grafana.Username, grafana.Password)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := grafana.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("Grafana is unreachable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return response.StatusCode, fmt.Errorf("Grafana rejected the managed administrator credential (%d)", response.StatusCode)
	}
	if result != nil && response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(result); err != nil {
			return response.StatusCode, fmt.Errorf("decode the Grafana response: %w", err)
		}
	}
	return response.StatusCode, nil
}

// ExperimentURL opens the live dashboard on one namespace from a start time.
// An empty namespace lets the dashboard select the namespace receiving load.
func ExperimentURL(baseURL, namespace string, from time.Time) string {
	query := url.Values{"refresh": {"10s"}, "to": {"now"}}
	if namespace != "" {
		query.Set("var-namespace", namespace)
	}
	if from.IsZero() {
		query.Set("from", "now-30m")
	} else {
		query.Set("from", strconv.FormatInt(from.UnixMilli(), 10))
	}
	return strings.TrimRight(baseURL, "/") + "/d/" + ExperimentUID + "?" + query.Encode()
}
