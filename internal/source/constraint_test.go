package source_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

func TestValidateConstraint(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{in: "", ok: true},
		{in: "1.2.3", ok: true},
		{in: "0.0.0", ok: true},
		{in: "^1", ok: true},
		{in: "^1.2", ok: true},
		{in: "^1.2.3", ok: true},
		{in: "^0", ok: true},
		{in: "^0.2", ok: true},
		{in: "1"}, {in: "1.2"}, {in: "1.2.3.4"}, {in: "^1.2.3.4"},
		{in: "^"}, {in: "^1."}, {in: "1..3"}, {in: "01.2.3"}, {in: "^1.02"},
		{in: "v1.2.3"}, {in: "1.2.3-rc.1"}, {in: "1.2.3+build"},
		{in: "~1.2.3"}, {in: ">=1.2.3"}, {in: "*"}, {in: "latest"},
		{in: "main"}, {in: "1.2.3 || 2.0.0"}, {in: " 1.2.3"}, {in: "^1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			err := source.ValidateConstraint(tt.in)
			if tt.ok {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "invalid version constraint") {
				t.Fatalf("ValidateConstraint(%q) = %v, want invalid version constraint", tt.in, err)
			}
		})
	}
}

func TestSatisfies(t *testing.T) {
	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		{constraint: "", version: "2.0.0", want: true},
		{constraint: "1.2.3", version: "1.2.3", want: true},
		{constraint: "1.2.3", version: "1.2.4"},
		{constraint: "^1", version: "1.0.0", want: true},
		{constraint: "^1", version: "1.99.99", want: true},
		{constraint: "^1", version: "0.9.9"},
		{constraint: "^1", version: "2.0.0"},
		{constraint: "^1.2", version: "1.1.99"},
		{constraint: "^1.2", version: "1.2.0", want: true},
		{constraint: "^1.2", version: "1.10.0", want: true},
		{constraint: "^1.2.3", version: "1.2.2"},
		{constraint: "^1.2.3", version: "1.2.3", want: true},
		{constraint: "^1.2.3", version: "1.3.0", want: true},
		{constraint: "^0", version: "0.0.0", want: true},
		{constraint: "^0", version: "0.99.0", want: true},
		{constraint: "^0", version: "1.0.0"},
		{constraint: "^0.2", version: "0.1.99"},
		{constraint: "^0.2", version: "0.2.0", want: true},
		{constraint: "^0.2", version: "0.9.0", want: true},
		{constraint: "^0.2.3", version: "0.2.2"},
		{constraint: "^0.2.3", version: "0.3.0", want: true},
		{constraint: "^0.0.3", version: "0.1.0", want: true},
		{constraint: "^0.0.3", version: "1.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.constraint+"/"+tt.version, func(t *testing.T) {
			got, err := source.Satisfies(tt.constraint, tt.version)
			if err != nil || got != tt.want {
				t.Fatalf("Satisfies(%q, %q) = %v, %v; want %v", tt.constraint, tt.version, got, err, tt.want)
			}
		})
	}
}

func TestSatisfiesRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		constraint string
		version    string
		errSub     string
	}{
		{constraint: "~1", version: "1.2.3", errSub: "invalid version constraint"},
		{constraint: "^1", version: "", errSub: `invalid version ""`},
		{constraint: "^1", version: "1.2", errSub: `invalid version "1.2"`},
		{constraint: "^1", version: "v1.2.3", errSub: "invalid version"},
		{constraint: "^1", version: "1.2.3-rc.1", errSub: "invalid version"},
		{constraint: "^1", version: "1.2.3+build", errSub: "invalid version"},
		{constraint: "^1", version: "01.2.3", errSub: "invalid version"},
		{constraint: "^1", version: "^1.2.3", errSub: "invalid version"},
		{constraint: "", version: "main", errSub: "invalid version"},
	}
	for _, tt := range tests {
		t.Run(tt.constraint+"/"+tt.version, func(t *testing.T) {
			got, err := source.Satisfies(tt.constraint, tt.version)
			if got || err == nil || !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("Satisfies(%q, %q) = %v, %v; want error containing %q", tt.constraint, tt.version, got, err, tt.errSub)
			}
		})
	}
}

func TestExtractedLegacySkillStillParses(t *testing.T) {
	s, commit := extractFixture(t)
	dest := filepath.Join(t.TempDir(), "review")
	if err := s.Extract("review", commit, dest); err != nil {
		t.Fatal(err)
	}
	meta, err := skill.Validate(dest)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "review" || meta.Version != "1.0.0" || meta.Requires != nil {
		t.Fatalf("legacy metadata = %+v", meta)
	}
}
