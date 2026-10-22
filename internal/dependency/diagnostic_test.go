package dependency_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
)

func TestAnalyzeGraphMalformedDetachedSubtrees(t *testing.T) {
	nodes := []dependency.Node{
		{Name: "root", Source: "team", Version: "1.0.0", Direct: true, Requires: []string{"left@^1", "right@^1", "missing@^1", "bad"}},
		{Name: "left", Source: "team", Version: "1.0.0", Requires: []string{"leaf@^1", "leaf@^1.0"}},
		{Name: "right", Source: "team", Version: "1.0.0", Requires: []string{"leaf@^1"}},
		{Name: "leaf", Source: "team", Version: "1.0.0", Direct: true},
		{Name: "orphan", Source: "team", Version: "1.0.0", Requires: []string{"abandoned@^1"}},
		{Name: "abandoned", Source: "team", Version: "1.0.0", Requires: []string{"orphan@^1"}},
	}
	original := make([]dependency.Node, len(nodes))
	for i, n := range nodes {
		original[i] = n
		original[i].Requires = slices.Clone(n.Requires)
	}
	for range 3 {
		info := dependency.AnalyzeGraph(nodes)
		if !slices.Equal(info.Orphans, []string{"abandoned", "orphan"}) || !slices.Equal(info.Via["leaf"], []string{"left", "right"}) {
			t.Fatalf("graph: %+v", info)
		}
		const cycle = "dependency cycle: team/abandoned@1.0.0 -> team/orphan@1.0.0 -> team/abandoned@1.0.0"
		if info.Cycle == nil || info.Cycle.Error() != cycle {
			t.Fatalf("cycle = %v, want %s", info.Cycle, cycle)
		}
		if !reflect.DeepEqual(nodes, original) {
			t.Fatal("analysis mutated input nodes")
		}
		slices.Reverse(nodes)
		slices.Reverse(original)
	}
}
