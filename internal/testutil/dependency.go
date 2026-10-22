package testutil

import (
	"fmt"
	"path/filepath"
	"testing"
)

// WriteSkillRequires writes a dependency-aware skill fixture without committing
// or tagging it. Unlike Release, it preserves the supplied requirement metadata.
func WriteSkillRequires(t testing.TB, repo, name, version string, requires []string) {
	t.Helper()
	front := "---\nname: " + name + "\ndescription: Test skill " + name + ".\nmetadata:\n"
	if version != "" {
		front += fmt.Sprintf("  version: %q\n", version)
	}
	if len(requires) == 0 {
		front += "  requires: []\n"
	} else {
		front += "  requires:\n"
		for _, ref := range requires {
			front += fmt.Sprintf("    - %q\n", ref)
		}
	}
	WriteFile(t, filepath.Join(repo, "skills", name, "SKILL.md"), front+"---\nTest skill.\n")
}
