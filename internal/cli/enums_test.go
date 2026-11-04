package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
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
// map has an entry for it. The sources are scanned for package-level maps with an agorav1 key or
// value, so a new map fails until it is covered here, and a covered map that is gone fails too.
func TestEveryEnumValueHasAWord(t *testing.T) {
	covered := map[string]func(){
		"bridgeStateNames":   func() { mapsCoverEnum(t, "bridgeStateNames", bridgeStateNames) },
		"bridgePolicyNames":  func() { mapsCoverEnum(t, "bridgePolicyNames", bridgePolicyNames) },
		"policyHints":        func() { mapsCoverEnum(t, "policyHints", policyHints) },
		"proposalStateNames": func() { mapsCoverEnum(t, "proposalStateNames", proposalStateNames) },
		"voteChoiceNames":    func() { mapsCoverEnum(t, "voteChoiceNames", voteChoiceNames) },
		"mechanismNames":     func() { mapsCoverEnum(t, "mechanismNames", mechanismNames) },
		"sessionStateNames":  func() { mapsCoverEnum(t, "sessionStateNames", sessionStateNames) },
		"deliveryStates":     func() { mapsCoverEnum(t, "deliveryStates", deliveryStates) },
		"subscriptionModes":  func() { mapsCoverEnum(t, "subscriptionModes (by value)", invert(subscriptionModes)) },
	}
	found := enumMaps(t)
	for _, name := range found {
		if _, ok := covered[name]; !ok {
			t.Errorf("%s maps an API enum but TestEveryEnumValueHasAWord does not check it; add it", name)
		}
	}
	for name, check := range covered {
		if !slices.Contains(found, name) {
			t.Errorf("%s is checked but no package-level map of that name uses an API enum", name)
			continue
		}
		check()
	}
}

// enumMaps returns the names of the package-level variables of this package's sources whose map
// literal has an agorav1 type as key or value.
func enumMaps(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.CompositeLit)
					if !ok {
						continue
					}
					m, ok := lit.Type.(*ast.MapType)
					if !ok {
						continue
					}
					if strings.Contains(types.ExprString(m.Key), "agorav1.") || strings.Contains(types.ExprString(m.Value), "agorav1.") {
						names = append(names, id.Name)
					}
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("no map of an API enum found: the scan looks at the wrong files")
	}
	return names
}

// invert turns the name to value map around, so the guard reaches it.
func invert[K comparable, V comparable](m map[K]V) map[V]K {
	out := make(map[V]K, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}
