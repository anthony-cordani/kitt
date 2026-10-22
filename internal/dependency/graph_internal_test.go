package dependency

import (
	"errors"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
)

func TestPlannerSearchBudget(t *testing.T) {
	raw := []string{}
	seeds := map[string]*manifest.Skill{
		"a": {Source: "team", Resolved: &manifest.Resolved{Version: "1.0.0", Commit: "a", Requires: &raw}},
		"b": {Source: "public"},
	}
	p := &planner{
		explored: maxSearchStates - 1,
		seeds:    seeds,
		current:  &manifest.Manifest{Skills: seeds},
		indexes:  make(map[string]map[string]int),
	}
	result, err := p.search(map[string]assignment{})
	var budget *SearchBudgetError
	if result != nil || !errors.As(err, &budget) || budget.Explored != maxSearchStates || p.explored != maxSearchStates || strings.Join(budget.Skills, ",") != "a,b" {
		t.Fatalf("budget=%#v result=%v err=%v", budget, result, err)
	}
	if !strings.Contains(err.Error(), "dependency search budget exhausted after 10000 states") {
		t.Fatal(err)
	}
}
