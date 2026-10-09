// Package install pins and installs skills from a kitt manifest.
package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

const (
	gitignoreStart = "# >>> kitt (managed by kitt, do not edit)"
	gitignoreEnd   = "# <<< kitt"
	tempPrefix     = ".kitt-tmp-"
)

// Options selects where skills are installed.
type Options struct {
	Global bool      // user level instead of project
	Root   string    // project root (used when Global is false)
	Out    io.Writer // progress messages
}

// Restore installs every skill of the manifest exactly as pinned, pinning entries that are not yet resolved.
func Restore(opts Options) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := load(p)
	if err != nil {
		return err
	}
	if err := validateManifestNames(m); err != nil {
		return err
	}
	p.installed, err = readInstalled(p, m)
	if err != nil {
		return err
	}
	out := outputOf(opts.Out)
	dirty := false
	for _, name := range sortedNames(m.Skills) {
		if err := p.installed.check(name); err != nil {
			return err
		}
		sk := m.Skills[name]
		if sk == nil {
			return fmt.Errorf("skill %s: nil manifest entry", name)
		}
		url, ok := m.Sources[sk.Source]
		if !ok {
			return fmt.Errorf("skill %s: unknown source %s", name, sk.Source)
		}
		if sk.Resolved == nil {
			src, err := openFetch(sk.Source, url)
			if err != nil {
				return err
			}
			resolved, err := src.Resolve(name, sk.Version)
			if err != nil {
				return fmt.Errorf("resolve skill %s: %w", name, err)
			}
			sum, err := installAt(p, name, resolved.Commit, "", src)
			if err != nil {
				return err
			}
			if sk.Version == "" && resolved.Version != "" {
				sk.Version = "^" + resolved.Version
			}
			sk.Resolved = &manifest.Resolved{
				Version: resolved.Version,
				Commit:  resolved.Commit,
				Hash:    sum,
			}
			dirty = true
			fmt.Fprintf(out, "pinned %s %s\n", name, versionLabel(sk.Resolved.Version, sk.Resolved.Commit))
			continue
		}
		dest := filepath.Join(p.skillsDir, name)
		match, err := hashMatches(dest, sk.Resolved.Hash)
		if err != nil {
			return err
		}
		if match {
			fmt.Fprintf(out, "ok %s %s\n", name, versionLabel(sk.Resolved.Version, sk.Resolved.Commit))
			continue
		}
		src, err := source.Open(sk.Source, url)
		if err != nil {
			return fmt.Errorf("open source %s: %w", sk.Source, err)
		}
		if !src.HasCommit(sk.Resolved.Commit) {
			if err := src.Fetch(); err != nil {
				return fmt.Errorf("fetch source %s: %w", sk.Source, err)
			}
			if !src.HasCommit(sk.Resolved.Commit) {
				return fmt.Errorf("skill %s: commit %s not found in %s", name, shortCommit(sk.Resolved.Commit), sk.Source)
			}
		}
		if _, err := installAt(p, name, sk.Resolved.Commit, sk.Resolved.Hash, src); err != nil {
			return err
		}
		fmt.Fprintf(out, "installed %s %s\n", name, versionLabel(sk.Resolved.Version, sk.Resolved.Commit))
	}
	if err := finish(p, sortedNames(m.Skills), true, out); err != nil {
		return err
	}
	if dirty {
		if err := m.Save(p.manifest); err != nil {
			return err
		}
	}
	return nil
}

// Add resolves ref ("[source/]skill[@constraint]"), pins it in the manifest and installs it.
func Add(opts Options, ref string) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := load(p)
	if err != nil {
		return err
	}
	if err := validateManifestNames(m); err != nil {
		return err
	}
	p.installed, err = readInstalled(p, m)
	if err != nil {
		return err
	}
	alias, name, constraint := parseRef(ref)
	if alias == "" {
		switch len(m.Sources) {
		case 0:
			return fmt.Errorf("no source in %s: add a [sources] table", p.manifest)
		case 1:
			for key := range m.Sources {
				alias = key
			}
		default:
			return fmt.Errorf("several sources: use <source>/<skill>")
		}
	}
	url, ok := m.Sources[alias]
	if !ok {
		return fmt.Errorf("unknown source %s", alias)
	}
	if err := skill.ValidName(name); err != nil {
		return err
	}
	if strings.HasPrefix(name, "kitt-") {
		return fmt.Errorf("skill names starting with kitt- are reserved")
	}
	if err := p.installed.check(name); err != nil {
		return err
	}
	src, err := openFetch(alias, url)
	if err != nil {
		return err
	}
	resolved, err := src.Resolve(name, constraint)
	if err != nil {
		return fmt.Errorf("resolve skill %s: %w", name, err)
	}
	sum, err := installAt(p, name, resolved.Commit, "", src)
	if err != nil {
		return err
	}
	version := constraint
	if constraint == "" && resolved.Version != "" {
		version = "^" + resolved.Version
	}
	m.Skills[name] = &manifest.Skill{
		Source:  alias,
		Version: version,
		Resolved: &manifest.Resolved{
			Version: resolved.Version,
			Commit:  resolved.Commit,
			Hash:    sum,
		},
	}
	if err := m.Save(p.manifest); err != nil {
		return err
	}
	out := outputOf(opts.Out)
	fmt.Fprintf(out, "added %s %s\n", name, versionLabel(resolved.Version, resolved.Commit))
	if err := finish(p, sortedNames(m.Skills), false, out); err != nil {
		return err
	}
	return nil
}

// AddSources adds sources to the user-level kitt.toml, creating the file when it is missing.
// Every URL is checked before anything is written; an alias already set to another URL is an error.
func AddSources(opts Options, sources map[string]string) error {
	if !opts.Global {
		return fmt.Errorf("--source needs -g: in a project, use kitt init --source")
	}
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := manifest.Load(p.manifest)
	created := false
	if errors.Is(err, fs.ErrNotExist) {
		m = &manifest.Manifest{Sources: map[string]string{}, Skills: map[string]*manifest.Skill{}}
		created = true
	} else if err != nil {
		return err
	}
	aliases := make([]string, 0, len(sources))
	for alias := range sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	var added []string
	for _, alias := range aliases {
		url := sources[alias]
		if existing, ok := m.Sources[alias]; ok {
			if existing != url {
				return fmt.Errorf("source %s already set to %s", alias, existing)
			}
			continue
		}
		if err := source.Check(url); err != nil {
			return fmt.Errorf("source %s: %s is not a reachable git repository", alias, url)
		}
		m.Sources[alias] = url
		added = append(added, alias)
	}
	if len(added) == 0 {
		return nil
	}
	if err := m.Save(p.manifest); err != nil {
		return err
	}
	if created {
		fmt.Fprintf(opts.Out, "created %s\n", p.manifest)
	}
	for _, alias := range added {
		fmt.Fprintf(opts.Out, "added source %s\n", alias)
	}
	return nil
}

// paths are the directories for one install target.
type paths struct {
	global    bool
	root      string
	manifest  string
	skillsDir string
	claudeDir string
	installed *installedState
}

func locate(opts Options) (paths, error) {
	if opts.Global {
		home, err := os.UserHomeDir()
		if err != nil {
			return paths{}, fmt.Errorf("locate home dir: %w", err)
		}
		mf, err := manifest.GlobalPath()
		if err != nil {
			return paths{}, err
		}
		return paths{
			global:    true,
			root:      home,
			manifest:  mf,
			skillsDir: filepath.Join(home, ".agents", "skills"),
			claudeDir: filepath.Join(home, ".claude", "skills"),
		}, nil
	}
	return paths{
		root:      opts.Root,
		manifest:  filepath.Join(opts.Root, manifest.FileName),
		skillsDir: filepath.Join(opts.Root, ".agents", "skills"),
		claudeDir: filepath.Join(opts.Root, ".claude", "skills"),
	}, nil
}

func load(p paths) (*manifest.Manifest, error) {
	m, err := manifest.Load(p.manifest)
	if err == nil {
		return m, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		if p.global {
			return nil, fmt.Errorf("%s not found: run kitt install -g --source <alias>=<url> <skill>", p.manifest)
		}
		return nil, fmt.Errorf("kitt.toml not found in %s: run kitt init, or create it with a [sources] table", p.root)
	}
	return nil, err
}

func openFetch(alias, url string) (*source.Source, error) {
	src, err := source.Open(alias, url)
	if err != nil {
		return nil, fmt.Errorf("open source %s: %w", alias, err)
	}
	if err := src.Fetch(); err != nil {
		return nil, fmt.Errorf("fetch source %s: %w", alias, err)
	}
	return src, nil
}

// installAt extracts commit into the skills directory.
// expected is checked when non-empty. The returned hash is the installed tree.
func installAt(p paths, name, commit, expected string, src *source.Source) (string, error) {
	// ValidName before the name is joined onto a path.
	if err := skill.ValidName(name); err != nil {
		return "", err
	}
	if err := p.installed.check(name); err != nil {
		return "", err
	}
	skillsDir := p.skillsDir
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return "", fmt.Errorf("create skills dir: %w", err)
	}
	dest := filepath.Join(skillsDir, name)
	tmp := filepath.Join(skillsDir, tempPrefix+name)
	if err := os.RemoveAll(tmp); err != nil {
		return "", fmt.Errorf("remove temp dir: %w", err)
	}
	if err := src.Extract(name, commit, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("extract skill %s: %w", name, err)
	}
	meta, err := skill.ParseFile(filepath.Join(tmp, "SKILL.md"))
	if err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("skill %s: %w", name, err)
	}
	if meta.Name != name {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("skill %s: SKILL.md declares name %s", name, meta.Name)
	}
	sum, err := skill.Hash(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("skill %s: %w", name, err)
	}
	if expected != "" && sum != expected {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("skill %s: hash mismatch (kitt.toml pins %s, source gives %s)", name, expected, sum)
	}
	if err := os.RemoveAll(dest); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("remove skill %s: %w", name, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("install skill %s: %w", name, err)
	}
	if err := p.installed.add(name); err != nil {
		return "", err
	}
	return sum, nil
}

func hashMatches(dest, want string) (bool, error) {
	info, err := os.Lstat(dest)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat skill: %w", err)
	}
	if !info.IsDir() {
		return false, nil
	}
	got, err := skill.Hash(dest)
	if err != nil {
		return false, fmt.Errorf("hash skill %s: %w", filepath.Base(dest), err)
	}
	return got == want, nil
}

// parseRef splits "[source/]skill[@constraint]" on the first '/' and the first '@'.
func parseRef(ref string) (alias, name, constraint string) {
	rest := ref
	if i := strings.Index(ref, "/"); i >= 0 {
		alias = ref[:i]
		rest = ref[i+1:]
	}
	name = rest
	if i := strings.Index(rest, "@"); i >= 0 {
		name = rest[:i]
		constraint = rest[i+1:]
	}
	return alias, name, constraint
}

func versionLabel(version, commit string) string {
	if version != "" {
		return version
	}
	return shortCommit(commit)
}

func shortCommit(commit string) string {
	if len(commit) < 7 {
		return commit
	}
	return commit[:7]
}

func sortedNames(skills map[string]*manifest.Skill) []string {
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func outputOf(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// syncLinks makes .claude/skills point at each managed skill.
// Project installs also drop links kitt previously created for skills that are gone.
func syncLinks(p paths, names []string, previouslyInstalled map[string]bool, out io.Writer) error {
	if len(names) > 0 {
		if err := os.MkdirAll(p.claudeDir, 0o755); err != nil {
			return fmt.Errorf("create claude skills dir: %w", err)
		}
	}
	for _, name := range names {
		if err := ensureLink(p, name, out); err != nil {
			return err
		}
	}
	if p.global {
		return nil
	}
	return pruneLinks(p, names, previouslyInstalled)
}

func ensureLink(p paths, name string, out io.Writer) error {
	link := filepath.Join(p.claudeDir, name)
	dest := filepath.Join(p.skillsDir, name)
	if _, err := os.Lstat(link); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", link, err)
		}
		return createLink(p, link, dest, name)
	}
	if sameDir(link, dest) {
		return nil
	}
	fmt.Fprintf(out, "warning: %s is not managed by kitt, left untouched\n", link)
	return nil
}

func createLink(p paths, link, dest, name string) error {
	if runtime.GOOS == "windows" {
		abs, err := filepath.Abs(dest)
		if err != nil {
			return fmt.Errorf("resolve skill %s: %w", name, err)
		}
		if err := validateJunctionPaths(link, abs); err != nil {
			return err
		}
		cmd := exec.Command("cmd", "/c", "mklink", "/J", link, abs)
		msg, err := cmd.CombinedOutput()
		if err != nil {
			text := strings.TrimSpace(string(msg))
			if text != "" {
				return fmt.Errorf("create junction %s: %s: %w", link, text, err)
			}
			return fmt.Errorf("create junction %s: %w", link, err)
		}
		return nil
	}
	target, err := symlinkTarget(p, name)
	if err != nil {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("create symlink %s: %w", link, err)
	}
	return nil
}

// symlinkTarget is relative inside a project and absolute for a user install.
func symlinkTarget(p paths, name string) (string, error) {
	if p.global {
		return filepath.Abs(filepath.Join(p.skillsDir, name))
	}
	return "../../.agents/skills/" + name, nil
}

func pruneLinks(p paths, names []string, previouslyInstalled map[string]bool) error {
	entries, err := os.ReadDir(p.claudeDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read claude skills dir: %w", err)
	}
	absSkills, err := filepath.Abs(p.skillsDir)
	if err != nil {
		return fmt.Errorf("resolve skills dir: %w", err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if slices.Contains(names, name) || !previouslyInstalled[name] {
			continue
		}
		link := filepath.Join(p.claudeDir, name)
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		absTarget, err := absoluteLinkTarget(p.claudeDir, target)
		if err != nil {
			return err
		}
		if !withinDir(absSkills, absTarget) {
			continue
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("remove link %s: %w", link, err)
		}
	}
	return nil
}

func absoluteLinkTarget(claudeDir, target string) (string, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(claudeDir, target)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve link target: %w", err)
	}
	return abs, nil
}

func withinDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

// pruneSkills removes project skill directories that are not managed.
func pruneSkills(state *installedState, managed []string, out io.Writer) error {
	dir := state.dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read skills dir: %w", err)
	}
	names := make([]string, len(entries))
	for i, ent := range entries {
		names[i] = ent.Name()
	}
	sort.Strings(names)
	for _, name := range names {
		if slices.Contains(managed, name) || name == installedFile || strings.HasPrefix(name, tempPrefix) {
			continue
		}
		if !state.names[name] {
			fmt.Fprintf(out, "warning: .agents/skills/%s is not managed by kitt, left untouched\n", name)
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("remove %s: %w", name, err)
		}
		if err := state.remove(name); err != nil {
			return err
		}
		fmt.Fprintf(out, "removed %s\n", name)
	}
	for name := range state.names {
		if !slices.Contains(managed, name) {
			if err := state.remove(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeGitignore(root string, names []string, claudeManaged bool) error {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	updated, changed := mergeGitignore(string(data), names, claudeManaged)
	if !changed {
		return nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

func mergeGitignore(existing string, names []string, claudeManaged bool) (string, bool) {
	block := renderGitignore(names, claudeManaged)
	if start, end, ok := findManagedBlock(existing); ok {
		updated := existing[:start] + block + existing[end:]
		return updated, updated != existing
	}
	if existing == "" {
		return block, true
	}
	updated := existing
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += block
	return updated, updated != existing
}

func renderGitignore(names []string, claudeManaged bool) string {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString(gitignoreStart)
	b.WriteByte('\n')
	b.WriteString("/.agents/skills/")
	b.WriteByte('\n')
	for _, name := range sorted {
		b.WriteString("/.claude/skills/")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	if claudeManaged {
		b.WriteString("/CLAUDE.md\n")
	}
	b.WriteString(gitignoreEnd)
	b.WriteByte('\n')
	return b.String()
}

// findManagedBlock returns the byte range of the managed block, including the
// newline that ends the closing marker when it has one.
func findManagedBlock(s string) (start, end int, ok bool) {
	start = lineIndex(s, gitignoreStart)
	if start < 0 {
		return 0, 0, false
	}
	rest := skipLine(s, start)
	rel := lineIndex(s[rest:], gitignoreEnd)
	if rel < 0 {
		return 0, 0, false
	}
	return start, skipLine(s, rest+rel), true
}

func lineIndex(s, marker string) int {
	for from := 0; from <= len(s); {
		i := strings.Index(s[from:], marker)
		if i < 0 {
			return -1
		}
		abs := from + i
		if lineMatch(s, abs, len(marker)) {
			return abs
		}
		from = abs + 1
	}
	return -1
}

func lineMatch(s string, abs, n int) bool {
	if abs > 0 && s[abs-1] != '\n' {
		return false
	}
	end := abs + n
	if end > len(s) {
		return false
	}
	return end == len(s) || s[end] == '\n' || s[end] == '\r'
}

func skipLine(s string, i int) int {
	for i < len(s) && s[i] != '\n' {
		i++
	}
	if i < len(s) {
		return i + 1
	}
	return i
}

// sameDir reports whether two paths reach the same directory once links are followed.
// It compares file identities, not resolved paths: on Windows filepath.EvalSymlinks
// does not follow the junctions kitt creates.
func sameDir(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

// validateManifestNames runs before any manifest key can reach a filesystem path.
func validateManifestNames(m *manifest.Manifest) error {
	for _, name := range sortedNames(m.Skills) {
		if err := skill.ValidName(name); err != nil {
			reason := strings.TrimPrefix(err.Error(), fmt.Sprintf("invalid skill name %q: ", name))
			return fmt.Errorf("invalid skill name in kitt.toml: %q: %s", name, reason)
		}
		if strings.HasPrefix(name, "kitt-") {
			return fmt.Errorf("invalid skill name in kitt.toml: %q: skill names starting with kitt- are reserved", name)
		}
	}
	return nil
}

func validateJunctionPaths(link, target string) error {
	if strings.ContainsAny(link, "&|<>^%!\"") || strings.ContainsAny(target, "&|<>^%!\"") {
		return fmt.Errorf("cannot create a junction for a path containing one of & | < > ^ %% ! \"")
	}
	return nil
}
