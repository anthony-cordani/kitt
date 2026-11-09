package templates

import (
	"strings"
	"testing"
)

func TestAgentsTemplateEndsWithKittSection(t *testing.T) {
	data, err := FS.ReadFile("files/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), KittSection) || strings.Count(string(data), KittStart) != 1 {
		t.Fatalf("AGENTS.md must end with exactly one KittSection:\n%s", data)
	}
}
