package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestInstalledStateWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, installedFile)
	testutil.WriteFile(t, path, "old\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows loads file identities lazily; capture this identity before replacement.
	if !os.SameFile(before, before) {
		t.Fatal("cannot read original state identity")
	}
	state := &installedState{dir: dir, names: map[string]bool{"review": true, "adr": true}}
	if err := state.save(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("state overwritten in place instead of atomically replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "adr\nreview\n" {
		t.Fatalf("state = %q", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp state left behind: %v", entries)
	}
}
