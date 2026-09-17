package api

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestValidateFigureFormats(t *testing.T) {
	var imageValue bytes.Buffer
	if err := png.Encode(&imageValue, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	if err := validateFigure(imageValue.Bytes(), "png"); err != nil {
		t.Fatalf("valid PNG was rejected: %v", err)
	}
	if err := validateFigure([]byte("not a PNG"), "png"); err == nil {
		t.Fatal("invalid PNG was accepted")
	}
	if err := validateFigure([]byte("%PDF-1.4\n1 0 obj\nendobj\n%%EOF"), "pdf"); err != nil {
		t.Fatalf("valid PDF was rejected: %v", err)
	}
	if err := validateFigure([]byte("%PDF-1.4\ntruncated"), "pdf"); err == nil {
		t.Fatal("incomplete PDF was accepted")
	}
	if err := validateFigure(imageValue.Bytes(), "svg"); err == nil {
		t.Fatal("unsupported format was accepted")
	}
}

func TestFigureMetricNames(t *testing.T) {
	for _, name := range []string{"load.successful_rps", "deployment.ready_replicas", "cpu.cores"} {
		if !figureMetricPattern.MatchString(name) {
			t.Fatalf("metric %q was rejected", name)
		}
	}
	for _, name := range []string{"", "../unsafe", "metric with spaces"} {
		if figureMetricPattern.MatchString(name) {
			t.Fatalf("unsafe metric %q was accepted", name)
		}
	}
}
