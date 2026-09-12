package managedplatform

import (
	"context"
	"errors"
	"testing"
)

type namespaceRunner struct {
	value string
	err   error
}

func (runner namespaceRunner) Kubectl(context.Context, string, []byte, ...string) (string, error) {
	return runner.value, runner.err
}

func (namespaceRunner) Helm(context.Context, string, []byte, []byte, ...string) (string, error) {
	return "", nil
}

func TestNamespaceOwnershipDistinguishesAbsentUnownedAndOwned(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		owned   bool
		present bool
	}{
		{name: "absent"},
		{name: "unowned", value: `{"metadata":{"annotations":{}}}`, present: true},
		{name: "owned", value: `{"metadata":{"annotations":{"kubephos.dev/ownership-marker":"marker"}}}`, owned: true, present: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owned, present, err := namespaceOwnership(context.Background(), namespaceRunner{value: test.value}, "config", "namespace", "marker")
			if err != nil || owned != test.owned || present != test.present {
				t.Fatalf("got owned=%v present=%v err=%v", owned, present, err)
			}
		})
	}
}

func TestNamespaceOwnershipPreservesReadError(t *testing.T) {
	expected := errors.New("read failed")
	_, _, err := namespaceOwnership(context.Background(), namespaceRunner{err: expected}, "config", "namespace", "marker")
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}
