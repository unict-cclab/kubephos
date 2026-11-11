package loadsession

import (
	"os/exec"
	"strings"
	"testing"
)

func TestLocustExternalDistributionUsesWeightsAndPhaseTransitions(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	start := strings.Index(locustWrapper, "def choose_weighted(")
	end := strings.Index(locustWrapper, "def wait_for_request_slot(")
	script := "import random\nfrom urllib.parse import urlsplit, urljoin\nrandom.seed(7)\n" + `
elapsed=0
def profile_elapsed(): return elapsed
def active_step(steps, elapsed):
    for step in steps:
        if elapsed < step["durationSeconds"]: return step, elapsed
        elapsed -= step["durationSeconds"]
    return None, 0
targets=[{"url":"https://west.example/v1","zone":"West","weight":1},{"url":"https://east.example/v2","zone":"East","weight":1}]
` + locustWrapper[start:end] + `
geographic_steps=[{"type":"constant","durationSeconds":60,"weights":{"West":3,"East":1}}]
west=sum(choose_target().startswith("https://west.example") for _ in range(10000))
assert 7300 < west < 7700, west
geographic_steps=[{"type":"constant","durationSeconds":60,"weights":{"West":1,"East":0}},{"type":"linear","durationSeconds":60,"startWeights":{"West":1,"East":0},"endWeights":{"West":0,"East":1}}]
assert request_url("/health")=="https://west.example/v1/health"
elapsed=90
assert zone_weights_at(elapsed,["West","East"])=={"West":0.5,"East":0.5}
elapsed=119.999
assert all(choose_target()=="https://east.example/v2" for _ in range(10))
# A single legacy endpoint remains usable without a geographic profile.
geographic_steps=[]
targets=targets[:1]
assert request_url("health")=="https://west.example/v1/health"
`
	if output, err := exec.Command(python, "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("Locust endpoint distribution: %v\n%s", err, output)
	}
}
