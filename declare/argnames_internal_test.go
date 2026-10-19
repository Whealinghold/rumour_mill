package declare

import (
	"slices"
	"testing"
)

func TestArgAllowLists(t *testing.T) {
	for name, n := range map[string]ArgNames{
		"zero resolved": ArgNames{}.resolved(),
		"renamed":       {Discriminator: "id_part", DiscriminatorFrom: "id_part_from"},
	} {
		got := addFactArgs(n)
		for _, want := range append(slices.Clone(addFactBase), n.Discriminator, n.DiscriminatorFrom) {
			if !slices.Contains(got, want) {
				t.Errorf("%s: addFactArgs lacks %q: %v", name, want, got)
			}
		}
		if len(got) != len(addFactBase)+2 {
			t.Errorf("%s: addFactArgs = %v", name, got)
		}
		if k := knowsArgs(n); !slices.Equal(k, knowsBase) {
			t.Errorf("%s: knowsArgs = %v, want %v", name, k, knowsBase)
		}
	}
}
