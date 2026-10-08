package scaffold

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/templates"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

const nextLine = "next: open your AI assistant in this project, it will run the kitt-bootstrap skill"

func TestInitCreatesProjectFiles(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sources := demoSources(t)
	var buf bytes.Buffer
	if err := Init(Options{Root: repo, Sources: sources, Out: &buf}); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Load(filepath.Join(repo, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if m.Sources["demo"] != sources["demo"] {
		t.Fatalf("kitt.toml source demo = %q, want %q", m.Sources["demo"], sources["demo"])
	}

	agents, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"<!-- kitt:bootstrap -->",
		"<!-- kitt:docs:start -->",
		"<!-- kitt:docs:end -->",
	} {
		if !strings.Contains(string(agents), marker) {
			t.Errorf("AGENTS.md missing %s", marker)
		}
	}
	requireTemplate(t, repo, "RULES.md", "files/RULES.md")
	requireTemplate(t, repo, ".agents/docs/README.md", "files/docs/README.md")

	claude, err := os.ReadFile(filepath.Join(repo, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(claude) != templates.ClaudeMD {
		t.Fatalf("CLAUDE.md = %q, want %q", claude, templates.ClaudeMD)
	}

	for _, name := range []string{"post-merge", "post-checkout"} {
		requireKittHook(t, filepath.Join(repo, ".git", "hooks", name))
	}

	ignore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"# >>> kitt (managed by kitt, do not edit)",
		"/.agents/skills/",
		"# <<< kitt",
	} {
		if !strings.Contains(string(ignore), fragment) {
			t.Errorf(".gitignore missing %q\n%s", fragment, ignore)
		}
	}

	lines := outputLines(buf.String())
	if len(lines) == 0 || lines[len(lines)-1] != nextLine {
		t.Fatalf("output does not end with the next line:\n%s", buf.String())
	}
}

func TestInitSecondTimeKeepsEveryFile(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sources := demoSources(t)
	if err := Init(Options{Root: repo, Sources: sources, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, repo)

	var buf bytes.Buffer
	if err := Init(Options{Root: repo, Sources: sources, Out: &buf}); err != nil {
		t.Fatal(err)
	}
	lines := outputLines(buf.String())
	if len(lines) == 0 {
		t.Fatal("second init produced no output")
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "kept ") {
			t.Errorf("second init line %q is not a kept line", line)
		}
	}
	diffSnapshots(t, before, snapshotTree(t, repo))
}

func TestInitAppendsProjectMemoryToExistingAgentsOnce(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	original := "Existing project notes.\n"
	testutil.WriteFile(t, filepath.Join(repo, "AGENTS.md"), original)
	sources := demoSources(t)
	opts := Options{Root: repo, Sources: sources, Out: io.Discard}

	if err := Init(opts); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, filepath.Join(repo, "AGENTS.md"))
	if !strings.HasPrefix(first, original) {
		t.Fatalf("original text is not at the start:\n%s", first)
	}
	if strings.Count(first, "## Project memory") != 1 {
		t.Fatalf("project memory section count = %d\n%s", strings.Count(first, "## Project memory"), first)
	}
	if strings.Contains(first, "<!-- kitt:bootstrap -->") {
		t.Fatalf("bootstrap marker was added:\n%s", first)
	}

	if err := Init(opts); err != nil {
		t.Fatal(err)
	}
	second := readFile(t, filepath.Join(repo, "AGENTS.md"))
	if err := Init(opts); err != nil {
		t.Fatal(err)
	}
	third := readFile(t, filepath.Join(repo, "AGENTS.md"))
	if second != first {
		t.Fatal("second init changed AGENTS.md")
	}
	if third != first {
		t.Fatal("third init appended the project memory section again")
	}
}

func TestInitLeavesExistingRulesUntouched(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	body := "Custom rules stay.\n"
	path := filepath.Join(repo, "RULES.md")
	testutil.WriteFile(t, path, body)
	if err := Init(Options{Root: repo, Sources: demoSources(t), Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != body {
		t.Fatalf("RULES.md = %q, want %q", got, body)
	}
}

func TestInitAppendsKittBlockToCustomHooksPath(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	original := "#!/bin/sh\necho keep-me\n"
	testutil.WriteFile(t, filepath.Join(repo, ".husky", "post-checkout"), original)
	testutil.Git(t, repo, "config", "core.hooksPath", ".husky")

	if err := Init(Options{Root: repo, Sources: demoSources(t), Out: io.Discard}); err != nil {
		t.Fatal(err)
	}

	checkout := readFile(t, filepath.Join(repo, ".husky", "post-checkout"))
	if !strings.HasPrefix(checkout, original) {
		t.Fatalf("original hook lines were not kept:\n%s", checkout)
	}
	if strings.Count(checkout, "# >>> kitt (managed by kitt, do not edit)") != 1 {
		t.Fatalf("kitt block count = %d\n%s", strings.Count(checkout, "# >>> kitt"), checkout)
	}
	postMerge := filepath.Join(repo, ".husky", "post-merge")
	requireKittHook(t, postMerge)

	for _, name := range []string{"post-merge", "post-checkout"} {
		path := filepath.Join(repo, ".git", "hooks", name)
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("hook written under .git/hooks/%s", name)
		}
	}
}

func TestInitRejectsUnreachableSourceWithoutWriting(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	before := listTree(t, repo)
	missing := testutil.URL(filepath.Join(t.TempDir(), "no-such-repo"))
	err := Init(Options{
		Root:    repo,
		Sources: map[string]string{"demo": missing},
		Out:     io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "not a reachable git repository") {
		t.Fatalf("error = %v", err)
	}
	after := listTree(t, repo)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("directory listing changed\nbefore:\n%s\nafter:\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func TestInitRejectsChangingExistingSourceURL(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	urlA := testutil.URL(testutil.NewRepo(t))
	if err := Init(Options{Root: repo, Sources: map[string]string{"demo": urlA}, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, repo)
	urlB := testutil.URL(testutil.NewRepo(t))
	err := Init(Options{Root: repo, Sources: map[string]string{"demo": urlB}, Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "already set") {
		t.Fatalf("error = %v", err)
	}
	diffSnapshots(t, before, snapshotTree(t, repo))
	m, err := manifest.Load(filepath.Join(repo, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if m.Sources["demo"] != urlA {
		t.Fatalf("source demo = %q, want %q", m.Sources["demo"], urlA)
	}
}

func TestInitAddsNewSourceAlias(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	urlA := testutil.URL(testutil.NewRepo(t))
	if err := Init(Options{Root: repo, Sources: map[string]string{"demo": urlA}, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	urlB := testutil.URL(testutil.NewRepo(t))
	var buf bytes.Buffer
	if err := Init(Options{Root: repo, Sources: map[string]string{"extra": urlB}, Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if !containsLine(buf.String(), "added source extra") {
		t.Fatalf("output missing added source line:\n%s", buf.String())
	}
	m, err := manifest.Load(filepath.Join(repo, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if m.Sources["demo"] != urlA || m.Sources["extra"] != urlB {
		t.Fatalf("sources = %#v", m.Sources)
	}
}

func TestInitWarnsWhenDirectoryIsNotGitRepo(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := Init(Options{Root: dir, Sources: map[string]string{}, Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if !containsLine(buf.String(), "warning: not a git repository, git hooks not installed") {
		t.Fatalf("output missing warning:\n%s", buf.String())
	}
}

func TestPullRunsHookAndInstallsSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git hook execution is skipped on Windows")
	}
	// Build before IsolateHome. IsolateHome points HOME at a temp dir, and a
	// later go build would store a read-only module cache there.
	binDir := t.TempDir()
	buildKitt(t, filepath.Join(binDir, "kitt"))
	testutil.IsolateHome(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := testutil.NewRepo(t)
	testutil.Release(t, src, "review", "1.0.0")
	srcURL := testutil.URL(src)

	bare := t.TempDir()
	testutil.Git(t, bare, "init", "--bare", "-q", "-b", "main")
	project := testutil.NewRepo(t)
	if err := Init(Options{Root: project, Sources: map[string]string{"demo": srcURL}, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, project, "init")
	testutil.Git(t, project, "remote", "add", "origin", bare)
	testutil.Git(t, project, "push", "-u", "origin", "main")

	parent := t.TempDir()
	testutil.Git(t, parent, "clone", bare, "clone")
	clone := filepath.Join(parent, "clone")
	testutil.Git(t, clone, "config", "user.email", "test@kitt")
	testutil.Git(t, clone, "config", "user.name", "kitt test")
	if err := Init(Options{Root: clone, Sources: map[string]string{"demo": srcURL}, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(clone, ".agents", "skills", "review")
	if _, err := os.Lstat(skillDir); err == nil {
		t.Fatal("skill exists in the clone before pull")
	}

	if err := install.Add(install.Options{Root: project, Out: io.Discard}, "review"); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, project, "add review")
	testutil.Git(t, project, "push", "origin", "main")
	testutil.Git(t, clone, "pull")

	if _, err := os.Stat(skillDir); err != nil {
		t.Fatalf("git pull did not install .agents/skills/review: %v", err)
	}
}

func demoSources(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"demo": testutil.URL(testutil.NewRepo(t))}
}

func requireTemplate(t *testing.T, root, name, template string) {
	t.Helper()
	want, err := templates.FS.ReadFile(template)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s does not match the template", name)
	}
}

func requireKittHook(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "#!/bin/sh\n") {
		t.Fatalf("%s does not start with #!/bin/sh:\n%s", path, text)
	}
	for _, fragment := range []string{
		"# >>> kitt (managed by kitt, do not edit)",
		"kitt install",
		"# <<< kitt",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("%s missing %q\n%s", path, fragment, text)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s is not executable (%v)", path, info.Mode())
	}
}

func buildKitt(t *testing.T, bin string) {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").CombinedOutput()
	if err != nil {
		t.Fatalf("go env GOMOD: %v\n%s", err, out)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == os.DevNull {
		t.Fatalf("go env GOMOD = %q", mod)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/kitt")
	cmd.Dir = filepath.Dir(mod)
	if out, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
}

func outputLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func containsLine(s, want string) bool {
	for _, line := range outputLines(s) {
		if line == want {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var rels []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rels)
	return rels
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "symlink:" + target
			return nil
		}
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffSnapshots(t *testing.T, before, after map[string]string) {
	t.Helper()
	seen := map[string]bool{}
	var keys []string
	for key := range before {
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range after {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		b, bok := before[key]
		a, aok := after[key]
		switch {
		case !bok:
			t.Errorf("file created: %s", key)
		case !aok:
			t.Errorf("file removed: %s", key)
		case a != b:
			t.Errorf("file content changed: %s", key)
		}
	}
}
