package scaffold

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestHookBlockSkipsWorktreeWithoutManifest(t *testing.T) {
	if !strings.Contains(hookBlock, "if [ ! -f kitt.toml ]; then\n") {
		t.Fatalf("hook block has no kitt.toml guard:\n%s", hookBlock)
	}
	if strings.Contains(hookBlock, "exit") {
		t.Fatalf("hook block must not exit, it may sit inside a user hook:\n%s", hookBlock)
	}
}

func TestInitHookReplacesOldManagedBlockInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-checkout")
	const before = "#!/bin/sh\necho before\n"
	const after = "echo after\n"
	const oldBlock = "# >>> kitt (managed by kitt, do not edit)\n" +
		"if command -v kitt >/dev/null 2>&1; then\n" +
		"  kitt install >/dev/null || echo \"kitt: install failed, run kitt install\" >&2\n" +
		"else\n" +
		"  echo \"kitt: not in PATH, skills not synced\" >&2\n" +
		"fi\n" +
		"# <<< kitt\n"
	testutil.WriteFile(t, path, before+oldBlock+after)
	if err := initHook(path, "post-checkout", io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := before + hookBlock + after; string(got) != want {
		t.Fatalf("hook = %q, want %q", got, want)
	}
}

func TestHookWithoutManifestIsSilent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git hook execution is skipped on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "post-checkout")
	if err := initHook(path, "post-checkout", io.Discard); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", path)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Fatalf("hook printed output without kitt.toml: %q", out)
	}
}
