package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExperimentHistoryRejectsInvalidOffsetBeforeReadingStorage(t *testing.T) {
	for _, offset := range []string{"-1", "100001", "invalid", "1.5"} {
		t.Run(offset, func(t *testing.T) {
			response := httptest.NewRecorder()
			(&Server{}).listExperiments(response, httptest.NewRequest(http.MethodGet, "/api/v1/experiments?offset="+offset, nil))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400", response.Code)
			}
		})
	}
}
