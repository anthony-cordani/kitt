package dependency_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in   string
		want dependency.Ref
	}{
		{in: "review@1.2.3", want: dependency.Ref{Name: "review", Constraint: "1.2.3"}},
		{in: "review@^1", want: dependency.Ref{Name: "review", Constraint: "^1"}},
		{in: "review@^1.2", want: dependency.Ref{Name: "review", Constraint: "^1.2"}},
		{in: "review@^1.2.3", want: dependency.Ref{Name: "review", Constraint: "^1.2.3"}},
		{in: "team/code-review@^0.2", want: dependency.Ref{Source: "team", Name: "code-review", Constraint: "^0.2"}},
		{in: "other-source/a1@0.0.0", want: dependency.Ref{Source: "other-source", Name: "a1", Constraint: "0.0.0"}},
		{in: "Team_1/review@^1", want: dependency.Ref{Source: "Team_1", Name: "review", Constraint: "^1"}},
		{in: "team.skills/review@^1", want: dependency.Ref{Source: "team.skills", Name: "review", Constraint: "^1"}},
		{in: "nexus-skills/review@^1", want: dependency.Ref{Source: "nexus-skills", Name: "review", Constraint: "^1"}},
		{in: "a.b_c/review@^1", want: dependency.Ref{Source: "a.b_c", Name: "review", Constraint: "^1"}},
		{in: strings.Repeat("a", 64) + "@^0", want: dependency.Ref{Name: strings.Repeat("a", 64), Constraint: "^0"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := dependency.ParseRef(tt.in)
			if err != nil || got != tt.want {
				t.Fatalf("ParseRef(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
			}
			if got.String() != tt.in {
				t.Fatalf("String = %q, want %q", got.String(), tt.in)
			}
		})
	}
}

func TestParseRefRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		in     string
		errSub string
	}{
		{in: "", errSub: "missing @constraint"},
		{in: "review", errSub: "missing @constraint"},
		{in: "team/review", errSub: "missing @constraint"},
		{in: "review@", errSub: "constraint must not be empty"},
		{in: "team/review@", errSub: "constraint must not be empty"},
		{in: "review@@^1", errSub: "exactly one @"},
		{in: "team@other/review@^1", errSub: "exactly one @"},
		{in: "@^1", errSub: "skill name must not be empty"},
		{in: "team/@^1", errSub: "skill name must not be empty"},
		{in: "/review@^1", errSub: "source alias must not be empty"},
		{in: "a/b/c@^1", errSub: "not a path"},
		{in: "./review@^1", errSub: `invalid source alias "."`},
		{in: "../review@^1", errSub: `invalid source alias ".."`},
		{in: "~/review@^1", errSub: `invalid source alias "~"`},
		{in: "~alice/review@^1", errSub: `invalid source alias "~alice"`},
		{in: "$VAR/review@^1", errSub: `invalid source alias "$VAR"`},
		{in: "%x/review@^1", errSub: `invalid source alias "%x"`},
		{in: "-team/review@^1", errSub: `invalid source alias "-team"`},
		{in: ".hidden/review@^1", errSub: `invalid source alias ".hidden"`},
		{in: "C:/review@^1", errSub: `invalid source alias "C:"`},
		{in: "team=url/review@^1", errSub: `invalid source alias "team=url"`},
		{in: "..", errSub: "missing @constraint"},
		{in: "..@^1", errSub: "invalid skill name"},
		{in: "team/..@^1", errSub: "invalid skill name"},
		{in: `team\review@^1`, errSub: "backslashes"},
		{in: " review@^1", errSub: "whitespace"},
		{in: "team /review@^1", errSub: "whitespace"},
		{in: "review@ ^1", errSub: "whitespace"},
		{in: "review@^1\n", errSub: "whitespace"},
		{in: "review@^1\t", errSub: "whitespace"},
		{in: "review\u00a0@^1", errSub: "whitespace"},
		{in: "te\x00am/review@^1", errSub: "control characters"},
		{in: "Review@^1", errSub: "invalid skill name"},
		{in: "re_view@^1", errSub: "invalid skill name"},
		{in: "re--view@^1", errSub: "invalid skill name"},
		{in: "-review@^1", errSub: "invalid skill name"},
		{in: "review-@^1", errSub: "invalid skill name"},
		{in: strings.Repeat("a", 65) + "@^1", errSub: "length must be 1-64"},
		{in: "review@1", errSub: "invalid version constraint"},
		{in: "review@1.2", errSub: "invalid version constraint"},
		{in: "review@v1.2.3", errSub: "invalid version constraint"},
		{in: "review@~1.2.3", errSub: "invalid version constraint"},
		{in: "review@>=1", errSub: "invalid version constraint"},
		{in: "review@*", errSub: "invalid version constraint"},
		{in: "review@latest", errSub: "invalid version constraint"},
		{in: "review@main", errSub: "invalid version constraint"},
		{in: "review@^", errSub: "invalid version constraint"},
		{in: "review@^1.2.3.4", errSub: "invalid version constraint"},
		{in: "review@1.2.3-rc.1", errSub: "invalid version constraint"},
		{in: "review@1.2.3+build", errSub: "invalid version constraint"},
		{in: "review@01.2.3", errSub: "invalid version constraint"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := dependency.ParseRef(tt.in)
			if got != (dependency.Ref{}) || err == nil || !strings.Contains(err.Error(), tt.errSub) || !strings.Contains(err.Error(), fmt.Sprintf("%q", tt.in)) {
				t.Fatalf("ParseRef(%q) = %+v, %v; want an error quoting input and containing %q", tt.in, got, err, tt.errSub)
			}
		})
	}
}

func TestWithDefaultSource(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "review@^1", want: "parent/review@^1"},
		{in: "other/review@^1", want: "other/review@^1"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			ref := mustParseRef(t, tt.in)
			got := ref.WithDefaultSource("parent")
			if got.String() != tt.want || ref.String() != tt.in {
				t.Fatalf("WithDefaultSource = %q, original = %q; want %q, %q", got, ref, tt.want, tt.in)
			}
		})
	}
}

func TestGroupRefs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want [][]string
	}{
		{name: "nil"},
		{name: "empty", in: []string{}},
		{name: "normalized_duplicate", in: []string{"review@^1", "parent/review@^1", "review@^1"}, want: [][]string{{"parent/review@^1"}}},
		{name: "all_constraints_retained", in: []string{"review@^1", "other@^2", "parent/review@1.2.3", "review@^2", "review@^1.0.0"}, want: [][]string{{"parent/review@^1", "parent/review@1.2.3", "parent/review@^2", "parent/review@^1.0.0"}, {"parent/other@^2"}}},
		{name: "sources_stay_distinct", in: []string{"review@^1", "other/review@^1", "parent/review@^1", "other/review@^2"}, want: [][]string{{"parent/review@^1"}, {"other/review@^1", "other/review@^2"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var refs []dependency.Ref
			for _, in := range tt.in {
				refs = append(refs, mustParseRef(t, in))
			}
			original := append([]dependency.Ref(nil), refs...)
			groups := dependency.GroupRefs(refs, "parent")
			var got [][]string
			for _, group := range groups {
				var names []string
				for _, ref := range group {
					names = append(names, ref.String())
				}
				got = append(got, names)
			}
			if !reflect.DeepEqual(got, tt.want) || !reflect.DeepEqual(refs, original) {
				t.Fatalf("GroupRefs = %#v, want %#v; input changed: %v", got, tt.want, !reflect.DeepEqual(refs, original))
			}
		})
	}
}

func mustParseRef(t *testing.T, s string) dependency.Ref {
	t.Helper()
	ref, err := dependency.ParseRef(s)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
