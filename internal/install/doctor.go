package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/templates"
	"go.yaml.in/yaml/v3"
)

// Doctor reports drift between the manifest, the installed skills, the links and the project files.
// It changes nothing and returns the number of problems found.
func Doctor(opts Options) (int, error) {
	p, err := locate(opts)
	if err != nil {
		return 0, err
	}
	m, err := load(p)
	if err != nil {
		return 0, err
	}
	agents := ""
	agentsExists := false
	if !p.global {
		agents, agentsExists, err = readAgents(p.root)
		if err != nil {
			return 0, err
		}
	}
	d := &doctor{
		p:       p,
		out:     outputOf(opts.Out),
		missing: map[string]bool{},
	}
	names := sortedNames(m.Skills)
	if err := d.skills(m, names); err != nil {
		return 0, err
	}
	managed := managedNames(p.global, names, agents)
	if err := d.links(managed); err != nil {
		return 0, err
	}
	if !p.global {
		if err := d.project(managed, agents, agentsExists); err != nil {
			return 0, err
		}
	}
	if d.problems == 0 {
		fmt.Fprintln(d.out, "ok")
	} else {
		fmt.Fprintf(d.out, "%d problem(s)\n", d.problems)
	}
	return d.problems, nil
}

// doctor is one read-only pass over a project or the user install.
type doctor struct {
	p        paths
	out      io.Writer
	problems int
	missing  map[string]bool
}

func (d *doctor) line(msg string) {
	fmt.Fprintln(d.out, msg)
	d.problems++
}

func (d *doctor) skills(m *manifest.Manifest, names []string) error {
	for _, name := range names {
		sk := m.Skills[name]
		if sk == nil {
			return fmt.Errorf("skill %s: nil manifest entry", name)
		}
		if sk.Resolved == nil {
			d.line(fmt.Sprintf("not pinned: %s (run kitt install)", name))
			continue
		}
		dest := filepath.Join(d.p.skillsDir, name)
		if _, err := os.Lstat(dest); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				d.line(fmt.Sprintf("missing: %s (run kitt install)", name))
				d.missing[name] = true
				continue
			}
			return fmt.Errorf("stat skill: %w", err)
		}
		match, err := hashMatches(dest, sk.Resolved.Hash)
		if err != nil {
			return err
		}
		if !match {
			d.line(fmt.Sprintf("modified: %s (local changes; kitt install restores the pinned version)", name))
		}
	}
	return nil
}

func (d *doctor) links(names []string) error {
	for _, name := range names {
		if d.missing[name] {
			continue
		}
		link := filepath.Join(d.p.claudeDir, name)
		shown := skillLink(d.p.global, name)
		if _, err := os.Lstat(link); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				d.line(fmt.Sprintf("missing link: %s (run kitt install)", shown))
				continue
			}
			return fmt.Errorf("stat %s: %w", link, err)
		}
		dest := filepath.Join(d.p.skillsDir, name)
		linked, errLink := filepath.EvalSymlinks(link)
		wanted, errDest := filepath.EvalSymlinks(dest)
		if errLink != nil || errDest != nil || linked != wanted {
			d.line(fmt.Sprintf("foreign entry: %s is not a kitt link", shown))
		}
	}
	return nil
}

func (d *doctor) project(names []string, agents string, agentsExists bool) error {
	claudeExists, claudeManaged, err := claudeState(d.p.root, agentsExists)
	if err != nil {
		return err
	}
	if err := d.gitignore(names, claudeManaged); err != nil {
		return err
	}
	if agentsExists && !claudeExists {
		d.line("missing: CLAUDE.md (run kitt install)")
	}
	if err := d.docs(agents); err != nil {
		return err
	}
	return d.staleDocs()
}

func (d *doctor) gitignore(names []string, claudeManaged bool) error {
	data, err := os.ReadFile(filepath.Join(d.p.root, ".gitignore"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	current := ""
	if start, end, ok := findManagedBlock(string(data)); ok {
		current = string(data)[start:end]
	}
	if current != renderGitignore(names, claudeManaged) {
		d.line("outdated: .gitignore kitt block (run kitt install)")
	}
	return nil
}

func (d *doctor) docs(agents string) error {
	start := exactLineIndex(agents, templates.DocsStart)
	if start < 0 {
		return nil
	}
	start = skipLine(agents, start)
	rel := exactLineIndex(agents[start:], templates.DocsEnd)
	if rel < 0 {
		return nil
	}
	index, err := docsIndex(filepath.Join(d.p.root, ".agents", "docs"))
	if err != nil {
		return err
	}
	if agents[start:start+rel] != index {
		d.line("outdated: AGENTS.md docs index (run kitt install)")
	}
	return nil
}

func (d *doctor) staleDocs() error {
	dir := filepath.Join(d.p.root, ".agents", "docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read project docs dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "README.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	commitID := regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	for _, name := range names {
		if err := d.staleDoc(dir, name, commitID); err != nil {
			return err
		}
	}
	return nil
}

func (d *doctor) staleDoc(dir, name string, commitID *regexp.Regexp) error {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("read project doc %s: %w", name, err)
	}
	body, ok := frontmatterYAML(string(data))
	if !ok {
		return nil
	}
	var meta struct {
		Paths    []string `yaml:"paths"`
		Verified string   `yaml:"verified"`
	}
	if err := yaml.Unmarshal([]byte(body), &meta); err != nil || len(meta.Paths) == 0 {
		return nil
	}
	rel := ".agents/docs/" + name
	if meta.Verified == "" {
		d.line(fmt.Sprintf("unverified doc: %s (set verified to a commit)", rel))
		return nil
	}
	if !commitID.MatchString(meta.Verified) {
		d.line(fmt.Sprintf("unverified doc: %s (verified is not a commit id)", rel))
		return nil
	}
	switch gitDiffStatus(d.p.root, meta.Verified, meta.Paths) {
	case gitChanged:
		d.line(fmt.Sprintf("stale doc: %s (%s changed since %s)", rel, strings.Join(meta.Paths, ", "), shortCommit(meta.Verified)))
	case gitFailed:
		d.line(fmt.Sprintf("unverified doc: %s (commit %s not found)", rel, shortCommit(meta.Verified)))
	}
	return nil
}

func managedNames(global bool, skillNames []string, agents string) []string {
	names := append([]string(nil), skillNames...)
	if !global && bootstrapActive(agents) && !slices.Contains(names, templates.BootstrapSkill) {
		names = append(names, templates.BootstrapSkill)
		sort.Strings(names)
	}
	return names
}

func bootstrapActive(agents string) bool {
	for _, line := range strings.Split(agents, "\n") {
		if strings.Trim(line, " \r") == templates.BootstrapMarker {
			return true
		}
	}
	return false
}

func readAgents(root string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read AGENTS.md: %w", err)
	}
	return string(data), true, nil
}

func claudeState(root string, agentsExists bool) (bool, bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("read CLAUDE.md: %w", err)
	}
	return true, agentsExists && string(data) == templates.ClaudeMD, nil
}

func skillLink(global bool, name string) string {
	if global {
		return "~/.claude/skills/" + name
	}
	return ".claude/skills/" + name
}

// frontmatterYAML returns the YAML between the opening and closing "---" lines.
// Line endings may be LF or CRLF. ok is false when the block is absent.
func frontmatterYAML(data string) (string, bool) {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 || strings.TrimSuffix(lines[0], "\r") != "---" {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if line == "---" {
			return b.String(), true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return "", false
}

const (
	gitSame = iota
	gitChanged
	gitFailed
)

func gitDiffStatus(root, verified string, paths []string) int {
	args := make([]string, 0, 6+len(paths))
	args = append(args, "-C", root, "diff", "--quiet", verified, "HEAD", "--")
	args = append(args, paths...)
	cmd := exec.Command("git", args...)
	cmd.Env = stripGitEnv(os.Environ())
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err == nil {
		return gitSame
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return gitChanged
	}
	return gitFailed
}

// stripGitEnv drops repository overrides so the diff reads the project at root.
func stripGitEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE":
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}
