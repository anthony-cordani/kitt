package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestInitHooksIgnoresGitRepositoryOverrides(t *testing.T) {
	for _, key := range []string{"GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Run(key, func(t *testing.T) {
			testutil.IsolateHome(t)
			repo := testutil.NewRepo(t)
			t.Setenv(key, filepath.Join(t.TempDir(), "missing"))
			var out bytes.Buffer
			if err := initHooks(repo, &out); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "post-checkout")); err != nil {
				t.Fatalf("hook missing: %v; output: %s", err, &out)
			}
		})
	}
}

func TestInitHookKeepsNonShellInterpreter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-checkout")
	const original = "#!/usr/bin/env python3\nprint('keep')\n"
	testutil.WriteFile(t, path, original)
	var out bytes.Buffer
	if err := initHook(path, "post-checkout", &out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("non-shell hook changed: %s", got)
	}
	if out.Len() == 0 {
		t.Fatal("no warning about unsupported interpreter")
	}
}
