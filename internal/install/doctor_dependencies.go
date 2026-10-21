package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

// dependencies reads only the selected manifest and installed files. In particular,
// it must never use planningManifest, which may obtain legacy metadata from Git.
func (d *doctor) dependencies(m *manifest.Manifest, names []string) error {
	inPlay := dependencyAware(m)
	if !inPlay {
		for _, name := range names {
			if !d.verified[name] {
				continue
			}
			meta, err := skill.ParseFile(filepath.Join(d.p.skillsDir, name, "SKILL.md"))
			if len(meta.Requires) > 0 || err != nil && strings.Contains(err.Error(), "metadata.requires") {
				inPlay = true
				break
			}
		}
	}
	if !inPlay {
		return nil
	}
	var nodes []dependency.Node
	known := make(map[string]bool)
	for _, name := range names {
		s := m.Skills[name]
		n := dependency.Node{Name: name, Source: s.Source, Direct: s.IsDirect()}
		if _, ok := m.Sources[s.Source]; !ok {
			d.line(fmt.Sprintf("wrong source: %s (unknown source alias %q in manifest)", name, s.Source))
		}
		if s.Resolved != nil {
			n.Version, n.Commit = s.Resolved.Version, s.Resolved.Commit
			if s.Resolved.Requires != nil {
				n.Requires = *s.Resolved.Requires
				known[name] = true
			}
			if d.verified[name] {
				meta, err := skill.ParseFile(filepath.Join(d.p.skillsDir, name, "SKILL.md"))
				if err != nil {
					d.line(fmt.Sprintf("invalid dependencies: %s (%v)", name, err))
				} else {
					installed, parseErrors := diagnosticRequirements(s.Source, meta.Requires)
					for _, err := range parseErrors {
						d.line(fmt.Sprintf("invalid dependencies: %s installed SKILL.md (%v)", name, err))
					}
					if s.Resolved.Requires == nil {
						n.Requires = installed
						known[name] = len(parseErrors) == 0
					} else {
						snapshot, snapshotErrors := diagnosticRequirements(s.Source, n.Requires)
						if len(parseErrors) > 0 || len(snapshotErrors) > 0 || !slices.Equal(snapshot, installed) {
							d.line(fmt.Sprintf("dependency snapshot disagrees: %s (resolved.requires differs from pinned SKILL.md; run kitt install)", name))
						}
					}
				}
			} else if s.Resolved.Requires == nil {
				d.line(fmt.Sprintf("dependency validation incomplete: %s (legacy pin has no requires snapshot and verified installed content is unavailable; run kitt install)", name))
			}
			if s.Version != "" {
				ok, err := source.Satisfies(s.Version, s.Resolved.Version)
				if err != nil || !ok {
					d.line(fmt.Sprintf("incompatible pin: %s@%s (manifest requests %q; clear its resolved block and run kitt install)", name, versionLabel(n.Version, n.Commit), s.Version))
				}
			}
		}
		var parseErrors []error
		n.Requires, parseErrors = diagnosticRequirements(s.Source, n.Requires)
		for _, err := range parseErrors {
			d.line(fmt.Sprintf("invalid dependencies: %s resolved.requires (%v)", name, err))
		}
		nodes = append(nodes, n)
	}
	for _, n := range nodes {
		for _, text := range n.Requires {
			r, _ := dependency.ParseRef(text)
			if _, ok := m.Sources[r.Source]; !ok {
				d.line(fmt.Sprintf("wrong source: %s requires %q (unknown source alias %q in manifest)", n.Name, text, r.Source))
			}
			s := m.Skills[r.Name]
			if s == nil {
				d.line(fmt.Sprintf("missing dependency entry: %s requires %q (restore skills.%s in the manifest)", n.Name, text, r.Name))
				continue
			}
			if s.Source != r.Source {
				d.line(fmt.Sprintf("wrong source: %s requires %q (skills.%s uses %q)", n.Name, text, r.Name, s.Source))
			}
			missing, err := dependencyFilesMissing(filepath.Join(d.p.skillsDir, r.Name))
			if err != nil {
				return err
			}
			if missing {
				d.line(fmt.Sprintf("missing dependency files: %s/%s (required by %s; run kitt install)", s.Source, r.Name, n.Name))
			}
			if s.Resolved != nil {
				ok, err := source.Satisfies(r.Constraint, s.Resolved.Version)
				if err != nil || !ok {
					d.line(fmt.Sprintf("incompatible pin: %s/%s@%s (%s requires %q; run kitt install or upgrade the dependent)", s.Source, r.Name, versionLabel(s.Resolved.Version, s.Resolved.Commit), n.Name, text))
				}
			}
		}
	}
	graph := dependency.AnalyzeGraph(nodes)
	if graph.Cycle != nil {
		d.line(graph.Cycle.Error())
	}
	for _, name := range names {
		var comparable, expected []string
		for _, parent := range m.Skills[name].Via {
			// Only saved snapshots govern via. Verified legacy frontmatter can
			// check dependencies, but an unchanged restore does not save via.
			if s := m.Skills[parent]; s == nil || s.Resolved != nil && s.Resolved.Requires != nil {
				comparable = append(comparable, parent)
			}
		}
		for _, parent := range graph.Via[name] {
			if s := m.Skills[parent]; s.Resolved != nil && s.Resolved.Requires != nil {
				expected = append(expected, parent)
			}
		}
		if !slices.Equal(comparable, expected) {
			d.line(fmt.Sprintf("stale via: %s (got %v; want %v; run kitt install)", name, m.Skills[name].Via, expected))
		}
	}
	// An unavailable reachable legacy node could lead to any otherwise detached
	// entry. Report incompleteness above instead of claiming those are orphans.
	for _, name := range names {
		if !known[name] && !slices.Contains(graph.Orphans, name) {
			return nil
		}
	}
	for _, name := range graph.Orphans {
		d.line(fmt.Sprintf("orphaned dependency: %s (unreachable from any direct skill; promote it or remove its manifest entry)", name))
	}
	return nil
}

func dependencyFilesMissing(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat dependency: %w", err)
	}
	if !info.IsDir() {
		return true, nil
	}
	info, err = os.Stat(filepath.Join(dir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat dependency skill file: %w", err)
	}
	return !info.Mode().IsRegular(), nil
}
