package cli

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// mapsCoverEnum fails for every value of the enum E, except its *_UNSPECIFIED value (the zero
// value of a field that was not set, which no map has an entry for), that m has no entry for.
func mapsCoverEnum[E interface {
	protoreflect.Enum
	comparable
}, V any](t *testing.T, name string, m map[E]V) {
	t.Helper()
	var zero E
	values := zero.Descriptor().Values()
	for i := range values.Len() {
		v := values.Get(i)
		if v.Number() == 0 {
			if got := string(v.Name()); !strings.HasSuffix(got, "_UNSPECIFIED") {
				t.Errorf("%s: value 0 is %s, not *_UNSPECIFIED; the test would skip a value that needs an entry", name, got)
			}
			continue
		}
		e, ok := zero.Type().New(v.Number()).(E)
		if !ok {
			t.Fatalf("%s: %s is not of the enum's Go type", name, v.Name())
		}
		if _, ok := m[e]; !ok {
			t.Errorf("%s has no entry for %s", name, v.Name())
		}
	}
}

// TestEveryEnumValueHasAWord fails when an API enum gains a value that a hand-written map from the
// enum to the words the CLI prints does not know. Every enum's *_UNSPECIFIED value is skipped: no
// map has an entry for it.
func TestEveryEnumValueHasAWord(t *testing.T) {
	mapsCoverEnum(t, "bridgeStateNames", bridgeStateNames)
	mapsCoverEnum(t, "bridgePolicyNames", bridgePolicyNames)
	mapsCoverEnum(t, "policyHints", policyHints)
	mapsCoverEnum(t, "proposalStateNames", proposalStateNames)
	mapsCoverEnum(t, "voteChoiceNames", voteChoiceNames)
	mapsCoverEnum(t, "mechanismNames", mechanismNames)
	mapsCoverEnum(t, "sessionStateNames", sessionStateNames)
	mapsCoverEnum(t, "deliveryStates", deliveryStates)
	mapsCoverEnum(t, "subscriptionModes (by value)", invert(subscriptionModes))
}

// invert turns the name to value map around, so the guard reaches it.
func invert[K comparable, V comparable](m map[K]V) map[V]K {
	out := make(map[V]K, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}
