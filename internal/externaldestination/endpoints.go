package externaldestination

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type Endpoint struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Normalize accepts the original single target as well as labeled destinations.
func Normalize(target string, endpoints []Endpoint) ([]Endpoint, error) {
	if len(endpoints) == 0 {
		endpoints = []Endpoint{{URL: target}}
	}
	if len(endpoints) > 50 {
		return nil, errors.New("Maximum: 50 external endpoints.")
	}
	result := make([]Endpoint, len(endpoints))
	seen := map[string]bool{}
	for i, endpoint := range endpoints {
		endpoint.URL = strings.TrimSpace(endpoint.URL)
		endpoint.Label = strings.TrimSpace(endpoint.Label)
		if endpoint.Label == "" {
			endpoint.Label = fmt.Sprintf("Endpoint %d", i+1)
		}
		key := strings.ToLower(endpoint.Label)
		if len(endpoint.Label) > 80 || strings.ContainsAny(endpoint.Label, "\n\r\t") || seen[key] {
			return nil, errors.New("Endpoint labels must be unique and at most 80 characters.")
		}
		seen[key] = true
		parsed, err := url.Parse(endpoint.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || len(endpoint.URL) > 2048 {
			return nil, fmt.Errorf("%s: use an HTTP or HTTPS URL without credentials or fragment.", endpoint.Label)
		}
		result[i] = endpoint
	}
	return result, nil
}
