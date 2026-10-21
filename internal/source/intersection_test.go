package source_test

import (
	"testing"

	"github.com/anthony-cordani/kitt/internal/source"
)

func TestConstraintsOverlap(t *testing.T) {
	for _, tc := range []struct {
		constraints []string
		want        bool
	}{
		{nil, true},
		{[]string{"", ""}, true},
		{[]string{"^1.2", "^1.9"}, true},
		{[]string{"^0.2", "^0.9"}, true},
		{[]string{"^1", "^2"}, false},
		{[]string{"^1.2", "1.2.0"}, true},
		{[]string{"1.1.0", "^1.2"}, false},
		{[]string{"1.2.0", "1.2.1"}, false},
		{[]string{"^0", "0.0.0"}, true},
	} {
		got, err := source.ConstraintsOverlap(tc.constraints)
		if err != nil || got != tc.want {
			t.Fatalf("ConstraintsOverlap(%v) = %v, %v; want %v", tc.constraints, got, err, tc.want)
		}
	}
	if _, err := source.ConstraintsOverlap([]string{"^1", "~1"}); err == nil {
		t.Fatal("invalid grammar accepted")
	}
}
