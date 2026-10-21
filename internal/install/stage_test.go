package install

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallDestinationPreflight(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project with spaces é")
	pathsAt := func(root string) paths {
		return paths{skillsDir: filepath.Join(root, ".agents", "skills"), claudeDir: filepath.Join(root, ".claude", "skills")}
	}
	if err := preflightDestination(pathsAt(root), "review"); err != nil {
		t.Fatalf("spaces and non-ASCII install root: %v", err)
	}
	if runtime.GOOS != "windows" {
		return
	}
	for _, tc := range []struct{ root, name, want string }{
		{root, "con", "reserved device name"},
		{root, "com1", "reserved device name"},
		{root + "&", "review", "cannot create a junction"},
		{filepath.Join(root, strings.Repeat("long", 65)), "review", "too long"},
	} {
		err := preflightDestination(pathsAt(tc.root), tc.name)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.name) || !strings.Contains(err.Error(), tc.root) {
			t.Fatalf("preflight %s/%s: %v", tc.root, tc.name, err)
		}
	}
}
