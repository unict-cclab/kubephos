package loadsession

import (
	"math"
	"os/exec"
	"strings"
	"testing"
)

func TestSinusoidalRangeValidationAndLegacy(t *testing.T) {
	ptr := func(n float64) *float64 { return &n }
	for _, tc := range []struct {
		name     string
		min, max *float64
		valid    bool
	}{
		{"zero minimum", ptr(0), ptr(80), true}, {"fractional", ptr(0.5), ptr(1.25), true},
		{"negative", ptr(-1), ptr(20), false}, {"equal", ptr(20), ptr(20), false},
		{"reversed", ptr(30), ptr(20), false}, {"missing maximum", ptr(0), nil, false},
		{"missing minimum", nil, ptr(30), false}, {"infinity", ptr(0), ptr(math.Inf(1)), false},
		{"legacy", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateLoadTimeline([]LoadStep{{Type: "sinusoidal", DurationSeconds: 120, MinRPS: tc.min, MaxRPS: tc.max, BaselineRPS: 20, AmplitudeRPS: 40, PeriodSeconds: 60}})
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestSinusoidalLocustCurveRangeAndLegacy(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	start := strings.Index(locustWrapper, "def active_step(")
	end := strings.Index(locustWrapper[start:], "def rps_at(") + start
	// Use the exact production rate function while isolating its clock and Locust dependencies.
	rate := locustWrapper[end:]
	next := strings.Index(rate[1:], "\ndef ")
	if next < 0 {
		t.Fatal("rate function boundary missing")
	}
	rate = rate[:next+1]
	script := "import math\n" + "def active_step(steps, elapsed):\n    return steps[0], elapsed\n" + rate + `
steps=[{"type":"sinusoidal","minRps":10,"maxRps":90,"periodSeconds":100}]
assert abs(rps_at(0)-50)<1e-9
assert abs(rps_at(25)-90)<1e-9
assert abs(rps_at(75)-10)<1e-9
steps=[{"type":"sinusoidal","baselineRps":20,"amplitudeRps":40,"periodSeconds":100}]
assert rps_at(0)==20
assert rps_at(25)==60
assert rps_at(75)==0
`
	if output, err := exec.Command(python, "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("curve: %v\n%s", err, output)
	}
}
