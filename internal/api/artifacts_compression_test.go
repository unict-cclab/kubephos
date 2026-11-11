package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"kubephos.dev/kubephos/internal/domain"
	"net/http/httptest"
	"testing"
)

func TestDatasetDownloadCompressionIsTransparentAndNegotiated(t *testing.T) {
	payload := bytes.Repeat([]byte(`{"timestamp":"2026-11-10T10:00:00Z","value":1.25},`), 1000)
	for _, tc := range []struct {
		name, kind, accept string
		compressed         bool
	}{
		{"browser", "TimeSeriesDataset", "gzip, deflate, br", true},
		{"disabled", "TimeSeriesDataset", "gzip;q=0, br", false},
		{"disabled decimal", "TimeSeriesDataset", "gzip;q=0.0", false},
		{"no header", "TimeSeriesDataset", "", false},
		{"other artifact", "ManifestSet", "gzip", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/download", nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			res := httptest.NewRecorder()
			writeArtifactBody(res, req, domain.Artifact{Type: tc.kind}, payload)
			body := res.Body.Bytes()
			if (res.Header().Get("Content-Encoding") == "gzip") != tc.compressed {
				t.Fatal("incorrect content encoding", res.Header())
			}
			if tc.compressed {
				if len(body) >= len(payload)/4 {
					t.Fatal("dataset not reduced")
				}
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(body, payload) {
				t.Fatal("dataset contents changed")
			}
		})
	}
}
