// Package testutil builds throwaway git repositories and an isolated home for tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// IsolateHome points every user directory kitt reads (home, cache, config) at a fresh temp dir,
// so tests never touch the real user cache or skills. It returns that home directory.
func IsolateHome(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("LocalAppData", filepath.Join(home, "AppData", "Local"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig-test"))
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	return home
}

// Git runs git in dir with a fixed identity and returns its trimmed output; it fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@kitt", "-c", "user.name=kitt test",
		"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
		"-c", "core.autocrlf=false",
		"-C", dir,
	}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewRepo creates an empty git repository on branch main and returns its path.
func NewRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	return dir
}

// URL returns the file:// URL of a local repository path, on every OS.
func URL(path string) string {
	slashed := filepath.ToSlash(path)
	if runtime.GOOS == "windows" || !strings.HasPrefix(slashed, "/") {
		return "file:///" + strings.TrimPrefix(slashed, "/")
	}
	return "file://" + slashed
}

// WriteSkill writes skills/<name>/SKILL.md in repo; version "" omits metadata.version.
func WriteSkill(t testing.TB, repo, name, version, body string) {
	t.Helper()
	front := "---\nname: " + name + "\ndescription: Test skill " + name + ".\n"
	if version != "" {
		front += "metadata:\n  version: \"" + version + "\"\n"
	}
	WriteFile(t, filepath.Join(repo, "skills", name, "SKILL.md"), front+"---\n"+body+"\n")
}

// WriteFile writes content to path, creating parent directories.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything in repo, commits and returns the new commit SHA.
func Commit(t testing.TB, repo, message string) string {
	t.Helper()
	Git(t, repo, "add", "-A")
	Git(t, repo, "commit", "-q", "-m", message)
	return Git(t, repo, "rev-parse", "HEAD")
}

// Release writes a skill version, commits it and tags it <name>/v<version>; it returns the commit SHA.
func Release(t testing.TB, repo, name, version string) string {
	t.Helper()
	WriteSkill(t, repo, name, version, name+" "+version)
	sha := Commit(t, repo, name+" "+version)
	Git(t, repo, "tag", name+"/v"+version)
	return sha
}
