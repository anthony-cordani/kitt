package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/templates"
)

type stagedSkill struct{ dir, hash string }

// Staging uses an existing ancestor of the install directory so validation does
// not create managed directories, and the final renames stay on the same volume.
func stagePlan(p paths, m *manifest.Manifest, plan *dependency.Plan, access *installSources) (staged map[string]stagedSkill, cleanup func(), err error) {
	root := ""
	cleanup = func() {
		if root != "" {
			os.RemoveAll(root)
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	staged = map[string]stagedSkill{}
	for _, n := range plan.Nodes {
		if err := skill.ValidName(n.Name); err != nil {
			return nil, cleanup, err
		}
		if strings.HasPrefix(n.Name, "kitt-") {
			return nil, cleanup, fmt.Errorf("skill %s: skill names starting with kitt- are reserved", n.Name)
		}
		if err := p.installed.check(n.Name); err != nil {
			return nil, cleanup, err
		}
		expected := ""
		if old := m.Skills[n.Name]; old != nil && old.Source == n.Source && old.Resolved != nil && old.Resolved.Commit == n.Commit {
			expected = old.Resolved.Hash
		}
		match, err := hashMatches(filepath.Join(p.skillsDir, n.Name), expected)
		if err != nil {
			return nil, cleanup, err
		}
		if match {
			staged[n.Name] = stagedSkill{hash: expected}
			continue
		}
		if root == "" {
			parent := p.skillsDir
			for {
				info, statErr := os.Stat(parent)
				if statErr == nil {
					if !info.IsDir() {
						return nil, cleanup, fmt.Errorf("install directory %s is not a directory", parent)
					}
					break
				}
				if !errors.Is(statErr, fs.ErrNotExist) {
					return nil, cleanup, statErr
				}
				parent = filepath.Dir(parent)
			}
			root, err = os.MkdirTemp(parent, tempPrefix+"plan-")
			if err != nil {
				return nil, cleanup, err
			}
		}
		src, err := access.exact(n.Source, n.Commit)
		if err != nil {
			return nil, cleanup, err
		}
		dir := filepath.Join(root, n.Name)
		if err := src.Extract(n.Name, n.Commit, dir); err != nil {
			return nil, cleanup, fmt.Errorf("extract skill %s: %w", n.Name, err)
		}
		meta, err := skill.Validate(dir)
		if err != nil {
			return nil, cleanup, fmt.Errorf("skill %s: %w", n.Name, err)
		}
		raw := make([]string, 0, len(meta.Requires))
		for _, text := range meta.Requires {
			r, err := dependency.ParseRef(text)
			if err != nil {
				return nil, cleanup, fmt.Errorf("skill %s: %w", n.Name, err)
			}
			raw = append(raw, r.WithDefaultSource(n.Source).String())
		}
		slices.Sort(raw)
		if !slices.Equal(slices.Compact(raw), n.Requires) {
			return nil, cleanup, fmt.Errorf("skill %s: resolved.requires snapshot disagrees with pinned frontmatter", n.Name)
		}
		sum, err := skill.Hash(dir)
		if err != nil {
			return nil, cleanup, err
		}
		if expected != "" && sum != expected {
			return nil, cleanup, fmt.Errorf("skill %s: hash mismatch (kitt.toml pins %s, source gives %s)", n.Name, expected, sum)
		}
		staged[n.Name] = stagedSkill{dir: dir, hash: sum}
	}
	return staged, cleanup, nil
}

func preflightInstall(p paths, plan *dependency.Plan) error {
	for _, dir := range []string{p.skillsDir, p.claudeDir, filepath.Dir(p.manifest)} {
		if err := preflightDirectory(dir); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(plan.Nodes)+1)
	for _, n := range plan.Nodes {
		names = append(names, n.Name)
	}
	if !p.global {
		agents, err := os.ReadFile(filepath.Join(p.root, "AGENTS.md"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read AGENTS.md: %w", err)
		}
		active := false
		for _, line := range strings.Split(string(agents), "\n") {
			active = active || strings.Trim(line, " \r") == templates.BootstrapMarker
		}
		if active {
			if err := p.installed.check(templates.BootstrapSkill); err != nil {
				return err
			}
			names = append(names, templates.BootstrapSkill)
		}
		for _, name := range []string{"CLAUDE.md", ".gitignore"} {
			if _, err := os.ReadFile(filepath.Join(p.root, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("read %s: %w", name, err)
			}
		}
		if exactLineIndex(string(agents), templates.DocsStart) >= 0 {
			if _, err := docsIndex(filepath.Join(p.root, ".agents", "docs")); err != nil {
				return err
			}
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "kitt-") && name != templates.BootstrapSkill {
			return fmt.Errorf("skill %s: skill names starting with kitt- are reserved", name)
		}
		link := filepath.Join(p.claudeDir, name)
		if _, err := os.Lstat(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", link, err)
		}
		if err := preflightDestination(p, name); err != nil {
			return err
		}
	}
	return nil
}

// Also used before metadata extraction: Windows device names cannot even be
// staged, and must produce a destination-specific diagnostic before Git access.
func preflightDestination(p paths, name string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	abs, err := filepath.Abs(filepath.Join(p.skillsDir, name))
	if err != nil {
		return err
	}
	link, err := filepath.Abs(filepath.Join(p.claudeDir, name))
	if err != nil {
		return err
	}
	if err := validateJunctionPaths(link, abs); err != nil {
		return fmt.Errorf("skill %s, link %s to %s: %w", name, link, abs, err)
	}
	for _, path := range []string{link, abs} {
		if len(utf16.Encode([]rune(path))) >= 260 {
			return fmt.Errorf("skill %s: install path %s is too long for kitt's Windows junction command; use a shorter install root", name, path)
		}
	}
	base := strings.ToUpper(name)
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return fmt.Errorf("skill %s: destination %s uses a Windows reserved device name", name, abs)
	}
	return nil
}

func preflightDirectory(dir string) error {
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("install path %s is not a directory", dir)
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat install path %s: %w", dir, err)
		}
		dir = filepath.Dir(dir)
	}
}

func applyStaged(p paths, plan *dependency.Plan, staged map[string]stagedSkill) error {
	nodes := map[string]dependency.Node{}
	for _, n := range plan.Nodes {
		nodes[n.Name] = n
	}
	done := map[string]bool{}
	var apply func(string) error
	apply = func(name string) error {
		if done[name] {
			return nil
		}
		done[name] = true // BuildPlan already ruled out cycles.
		for _, text := range nodes[name].Requires {
			r, _ := dependency.ParseRef(text)
			if err := apply(r.Name); err != nil {
				return err
			}
		}
		if s := staged[name]; s.dir != "" {
			if err := os.MkdirAll(p.skillsDir, 0o755); err != nil {
				return err
			}
			dest := filepath.Join(p.skillsDir, name)
			if err := os.RemoveAll(dest); err != nil {
				return fmt.Errorf("remove skill %s: %w", name, err)
			}
			if err := os.Rename(s.dir, dest); err != nil {
				return fmt.Errorf("install skill %s: %w", name, err)
			}
		}
		return p.installed.add(name)
	}
	for _, n := range plan.Nodes {
		if err := apply(n.Name); err != nil {
			return err
		}
	}
	return nil
}
