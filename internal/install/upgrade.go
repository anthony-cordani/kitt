package install

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
)

// Upgrade moves one skill (or all when name is "") to its newest version, showing what changes.
func Upgrade(opts Options, name string, major bool) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := load(p)
	if err != nil {
		return err
	}
	targets, err := upgradeTargets(m, p.manifest, name)
	if err != nil {
		return err
	}
	out := outputOf(opts.Out)
	opened := map[string]*source.Source{}
	dirty := false
	for _, skillName := range targets {
		changed, err := upgradeOne(p, m, opened, out, skillName, major)
		if err != nil {
			return err
		}
		if changed {
			dirty = true
		}
	}
	if dirty {
		if err := m.Save(p.manifest); err != nil {
			return err
		}
	}
	return finish(p, sortedNames(m.Skills), false, out)
}

func upgradeTargets(m *manifest.Manifest, manifestPath, name string) ([]string, error) {
	if name == "" {
		return sortedNames(m.Skills), nil
	}
	if _, ok := m.Skills[name]; !ok {
		return nil, fmt.Errorf("skill %s is not in %s", name, manifestPath)
	}
	return []string{name}, nil
}

// upgradeOne upgrades a single manifest skill.
// changed reports whether the manifest entry was updated.
func upgradeOne(p paths, m *manifest.Manifest, opened map[string]*source.Source, out io.Writer, name string, major bool) (bool, error) {
	sk := m.Skills[name]
	if sk == nil {
		return false, fmt.Errorf("skill %s: nil manifest entry", name)
	}
	src, err := fetchedSource(opened, m, sk.Source, name)
	if err != nil {
		return false, err
	}
	if exactPin(sk.Version) {
		fmt.Fprintf(out, "kept %s %s (exact pin in kitt.toml)\n", name, sk.Version)
		return false, nil
	}
	next, note, err := selectUpgrade(src, sk, name, major)
	if err != nil {
		return false, err
	}
	if note != "" {
		fmt.Fprintln(out, note)
	}
	if sk.Resolved != nil && next.Commit == sk.Resolved.Commit {
		fmt.Fprintf(out, "up to date %s %s\n", name, versionLabel(sk.Resolved.Version, sk.Resolved.Commit))
		return false, nil
	}
	if sk.Resolved != nil {
		diff, err := src.Diff(name, sk.Resolved.Commit, next.Commit)
		if err != nil {
			return false, fmt.Errorf("diff skill %s: %w", name, err)
		}
		fmt.Fprint(out, diff)
	}
	sum, err := installAt(p.skillsDir, name, next.Commit, "", src)
	if err != nil {
		return false, err
	}
	old := "-"
	if sk.Resolved != nil {
		old = versionLabel(sk.Resolved.Version, sk.Resolved.Commit)
	}
	sk.Resolved = &manifest.Resolved{
		Version: next.Version,
		Commit:  next.Commit,
		Hash:    sum,
	}
	if next.Version != "" {
		sk.Version = "^" + next.Version
	}
	fmt.Fprintf(out, "upgraded %s %s -> %s\n", name, old, versionLabel(next.Version, next.Commit))
	return true, nil
}

// fetchedSource opens and fetches each alias at most once per run.
func fetchedSource(opened map[string]*source.Source, m *manifest.Manifest, alias, name string) (*source.Source, error) {
	if src, ok := opened[alias]; ok {
		return src, nil
	}
	url, ok := m.Sources[alias]
	if !ok {
		return nil, fmt.Errorf("skill %s: unknown source %s", name, alias)
	}
	src, err := openFetch(alias, url)
	if err != nil {
		return nil, err
	}
	opened[alias] = src
	return src, nil
}

// selectUpgrade picks the commit to move to.
// note is the major-upgrade hint, empty when there is nothing to say.
func selectUpgrade(src *source.Source, sk *manifest.Skill, name string, major bool) (source.Resolved, string, error) {
	versions, err := src.Versions(name)
	if err != nil {
		return source.Resolved{}, "", fmt.Errorf("skill %s: %w", name, err)
	}
	if len(versions) == 0 {
		resolved, err := src.Resolve(name, "")
		if err != nil {
			return source.Resolved{}, "", fmt.Errorf("resolve skill %s: %w", name, err)
		}
		return resolved, "", nil
	}
	latest := versions[len(versions)-1]
	current := latest
	if sk.Resolved != nil && sk.Resolved.Version != "" {
		current = sk.Resolved.Version
	}
	currentMajor, err := versionMajor(current)
	if err != nil {
		return source.Resolved{}, "", fmt.Errorf("skill %s: %w", name, err)
	}
	latestMajor, err := versionMajor(latest)
	if err != nil {
		return source.Resolved{}, "", fmt.Errorf("skill %s: %w", name, err)
	}
	sameMajor := highestMajor(versions, currentMajor)
	note := ""
	chosen := sameMajor
	if latestMajor > currentMajor && !major {
		note = fmt.Sprintf("note: %s %s is available (major upgrade, use --major)", name, latest)
		chosen = sameMajor
	} else if major {
		chosen = latest
	}
	if chosen == "" {
		return source.Resolved{}, "", fmt.Errorf("skill %s: no version with major %d", name, currentMajor)
	}
	resolved, err := src.Resolve(name, chosen)
	if err != nil {
		return source.Resolved{}, "", fmt.Errorf("resolve skill %s: %w", name, err)
	}
	return resolved, note, nil
}

// highestMajor is the last version in the ascending list whose major is want.
func highestMajor(versions []string, want int) string {
	found := ""
	for _, v := range versions {
		maj, err := versionMajor(v)
		if err != nil || maj != want {
			continue
		}
		found = v
	}
	return found
}

// exactPin reports whether v is an exact X.Y.Z constraint, with no caret.
func exactPin(v string) bool {
	if strings.HasPrefix(v, "^") {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !plainNumber(part) {
			return false
		}
	}
	return true
}

func plainNumber(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// versionMajor returns the major component of a version or a caret constraint.
func versionMajor(v string) (int, error) {
	raw := strings.TrimPrefix(v, "^")
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		raw = raw[:i]
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid version %q", v)
	}
	return n, nil
}
