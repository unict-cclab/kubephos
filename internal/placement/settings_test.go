package placement

import "testing"

func TestPoolValidationProtectsManagedSettings(t *testing.T) {
	for _, key := range []string{"kubephos.dev/role", "topology.kubernetes.io/zone", "kubernetes.io/hostname", "node-role.kubernetes.io/control-plane"} {
		if ValidatePool(PoolSettings{Labels: map[string]string{key: "other"}}) == nil {
			t.Errorf("accepted managed label %s", key)
		}
		if ValidatePool(PoolSettings{Taints: []Taint{{Key: key, Effect: "NoSchedule"}}}) == nil {
			t.Errorf("accepted managed taint %s", key)
		}
	}
	for _, key := range []string{"node.kubernetes.io/not-ready", "node.cloudprovider.kubernetes.io/uninitialized"} {
		if ValidatePool(PoolSettings{Taints: []Taint{{Key: key, Effect: "NoExecute"}}}) == nil {
			t.Errorf("accepted system taint %s", key)
		}
	}
	for _, key := range []string{"gpu", "research.example/pool", "example.org/gpu"} {
		if err := ValidatePool(PoolSettings{Labels: map[string]string{key: ""}, Taints: []Taint{{Key: key, Effect: "NoExecute"}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"a/b/c", "example..org/key", "Upper.org/key", "-bad", "example.org/"} {
		if ValidKey(key) {
			t.Errorf("accepted invalid key %s", key)
		}
	}
	if err := ValidatePool(PoolSettings{Taints: []Taint{{Key: "gpu", Effect: "NoSchedule"}, {Key: "gpu", Value: "other", Effect: "NoSchedule"}}}); err == nil {
		t.Fatal("accepted duplicate key/effect")
	}
}
func TestTolerationValidation(t *testing.T) {
	seconds := int64(0)
	negative := int64(-1)
	for _, value := range []Toleration{{Key: "gpu"}, {Operator: "Exists"}, {Key: "gpu", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &seconds}} {
		if err := ValidateTolerations([]Toleration{value}); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []Toleration{{}, {Key: "gpu", Operator: "Other"}, {Key: "gpu", Operator: "Exists", Value: "true"}, {Key: "gpu", Effect: "NoSchedule", TolerationSeconds: &seconds}, {Key: "gpu", Effect: "NoExecute", TolerationSeconds: &negative}} {
		if ValidateTolerations([]Toleration{value}) == nil {
			t.Errorf("accepted invalid toleration %+v", value)
		}
	}
	if ValidateTolerations([]Toleration{{Key: "gpu"}, {Key: "gpu", Operator: "Equal"}}) == nil {
		t.Fatal("accepted duplicate default operator")
	}
}
func TestServiceModesDoNotMutateDefaults(t *testing.T) {
	settings := &ApplicationSettings{NodeSelector: map[string]string{"disk": "ssd"}, Tolerations: []Toleration{{Key: "gpu"}}, ServiceOverrides: map[string]ServiceOverride{"off": {NodeSelectorMode: "none", TolerationsMode: "none"}, "custom": {NodeSelectorMode: "custom", NodeSelector: map[string]string{"disk": "hdd"}, TolerationsMode: "custom"}}}
	selector, tols := Effective(settings, "base")
	if selector["disk"] != "ssd" || len(tols) != 1 {
		t.Fatal("defaults lost")
	}
	selector, tols = Effective(settings, "off")
	if len(selector)+len(tols) != 0 {
		t.Fatal("None inherited custom values")
	}
	selector, tols = Effective(settings, "custom")
	if selector["disk"] != "hdd" || len(tols) != 0 {
		t.Fatal("custom did not replace custom defaults")
	}
	if settings.NodeSelector["disk"] != "ssd" || len(settings.Tolerations) != 1 {
		t.Fatal("defaults changed")
	}
}
