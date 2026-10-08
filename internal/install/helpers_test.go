package install_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func withOpts(root string, global bool) (install.Options, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return install.Options{Root: root, Global: global, Out: buf}, buf
}

func newPair(t *testing.T) (project, skills string) {
	t.Helper()
	testutil.IsolateHome(t)
	return testutil.NewRepo(t), testutil.NewRepo(t)
}

func release(t *testing.T, repo, name string, versions ...string) {
	t.Helper()
	for _, version := range versions {
		testutil.Release(t, repo, name, version)
	}
}

// standard is a project whose only source is demo, with review 1.0.0, 1.4.0 and 2.0.0.
func standard(t *testing.T) (project, skills string) {
	t.Helper()
	project, skills = newPair(t)
	release(t, skills, "review", "1.0.0", "1.4.0", "2.0.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	return project, skills
}

func writeKitt(t *testing.T, project string, sources map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[sources]\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "%s = %q\n", key, sources[key])
	}
	testutil.WriteFile(t, filepath.Join(project, manifest.FileName), b.String())
}

func mustAdd(t *testing.T, root, ref string) string {
	t.Helper()
	opt, buf := withOpts(root, false)
	if err := install.Add(opt, ref); err != nil {
		t.Fatalf("Add %s: %v", ref, err)
	}
	return buf.String()
}

func mustRestore(t *testing.T, root string) string {
	t.Helper()
	opt, buf := withOpts(root, false)
	if err := install.Restore(opt); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return buf.String()
}

func mustUpgrade(t *testing.T, root, name string, major bool) string {
	t.Helper()
	opt, buf := withOpts(root, false)
	if err := install.Upgrade(opt, name, major); err != nil {
		t.Fatalf("Upgrade %q: %v", name, err)
	}
	return buf.String()
}

func mustList(t *testing.T, root string) string {
	t.Helper()
	opt, buf := withOpts(root, false)
	if err := install.List(opt); err != nil {
		t.Fatalf("List: %v", err)
	}
	return buf.String()
}

func mustDoctor(t *testing.T, root string) (int, string) {
	t.Helper()
	opt, buf := withOpts(root, false)
	n, err := install.Doctor(opt)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	return n, buf.String()
}

func loadManifest(t *testing.T, path string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func skillEntry(t *testing.T, project, name string) *manifest.Skill {
	t.Helper()
	sk := loadManifest(t, filepath.Join(project, manifest.FileName)).Skills[name]
	if sk == nil {
		t.Fatalf("manifest has no skill %s", name)
	}
	return sk
}

func assertResolved(t *testing.T, sk *manifest.Skill, constraint, version string) {
	t.Helper()
	if sk.Version != constraint {
		t.Fatalf("constraint = %q, want %q", sk.Version, constraint)
	}
	if sk.Resolved == nil {
		t.Fatal("resolved block is missing")
	}
	if sk.Resolved.Version != version {
		t.Fatalf("resolved version = %q, want %q", sk.Resolved.Version, version)
	}
	if !commitRE.MatchString(sk.Resolved.Commit) {
		t.Fatalf("commit = %q, want 40 hex chars", sk.Resolved.Commit)
	}
	if !strings.HasPrefix(sk.Resolved.Hash, "h1:") {
		t.Fatalf("hash = %q, want h1: prefix", sk.Resolved.Hash)
	}
}

func skillPath(root, name string) string {
	return filepath.Join(root, ".agents", "skills", name)
}

func claudePath(root, name string) string {
	return filepath.Join(root, ".claude", "skills", name)
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("%s still exists", path)
}

func assertLinkResolves(t *testing.T, link, dest string) {
	t.Helper()
	got, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", link, err)
	}
	want, err := filepath.EvalSymlinks(dest)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", dest, err)
	}
	if got != want {
		t.Fatalf("EvalSymlinks(%s) = %s, want %s", link, got, want)
	}
}

func assertNoTempDirs(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".kitt-tmp-") {
			t.Fatalf("temp dir remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// kittBlock is the managed .gitignore section for names, in name order.
func kittBlock(names []string, claude bool) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString("# >>> kitt (managed by kitt, do not edit)\n")
	b.WriteString("/.agents/skills/\n")
	for _, name := range sorted {
		b.WriteString("/.claude/skills/")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	if claude {
		b.WriteString("/CLAUDE.md\n")
	}
	b.WriteString("# <<< kitt\n")
	return b.String()
}

func lineWith(out string, fields ...string) bool {
	for _, line := range strings.Split(out, "\n") {
		ok := true
		for _, field := range fields {
			if !strings.Contains(line, field) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// snapshot walks root and records file bytes and link targets.
// .git is skipped: its stat cache is not project content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			got[rel] = "link:" + target
			return nil
		}
		if d.IsDir() {
			got[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got[rel] = "file:" + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertSameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	var diffs []string
	for key, value := range before {
		other, ok := after[key]
		if !ok {
			diffs = append(diffs, "removed "+key)
			continue
		}
		if other != value {
			diffs = append(diffs, "changed "+key)
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			diffs = append(diffs, "added "+key)
		}
	}
	if len(diffs) == 0 {
		return
	}
	sort.Strings(diffs)
	t.Fatalf("tree changed:\n%s", strings.Join(diffs, "\n"))
}
