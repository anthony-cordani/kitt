package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"text/tabwriter"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
)

// List prints the installed skills and the skills available in each source.
func List(opts Options) error {
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
	out := outputOf(opts.Out)
	fmt.Fprintln(out, "Installed:")
	names := sortedNames(m.Skills)
	if len(names) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else if dependencyAware(m) {
		if err := writeDependencyTree(out, p.skillsDir, m, names); err != nil {
			return err
		}
	} else if err := writeInstalled(out, p.skillsDir, m.Skills, names); err != nil {
		return err
	}
	aliases := make([]string, 0, len(m.Sources))
	for alias := range m.Sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if err := writeAvailable(out, m, alias); err != nil {
			return err
		}
	}
	return nil
}

func dependencyAware(m *manifest.Manifest) bool {
	for _, s := range m.Skills {
		if s != nil && (s.Direct != nil || s.Via != nil || s.Resolved != nil && s.Resolved.Requires != nil) {
			return true
		}
	}
	return false
}

func writeDependencyTree(out io.Writer, skillsDir string, m *manifest.Manifest, names []string) error {
	var nodes []dependency.Node
	children := make(map[string][]dependency.Ref)
	labels := make(map[string]string)
	for _, name := range names {
		s := m.Skills[name]
		if s == nil {
			return fmt.Errorf("skill %s: nil manifest entry", name)
		}
		label, err := installedLabel(skillsDir, name, s)
		if err != nil {
			return err
		}
		labels[name] = label
		n := dependency.Node{Name: name, Source: s.Source, Direct: s.IsDirect()}
		if s.Resolved != nil {
			n.Version, n.Commit = s.Resolved.Version, s.Resolved.Commit
			if label == "not installed" {
				labels[name] = versionLabel(n.Version, n.Commit) + " [not installed]"
			}
			if s.Resolved.Requires != nil {
				n.Requires = *s.Resolved.Requires
			} else {
				match, err := hashMatches(filepath.Join(skillsDir, name), s.Resolved.Hash)
				if err != nil {
					return err
				}
				if match {
					meta, err := skill.ParseFile(filepath.Join(skillsDir, name, "SKILL.md"))
					if err == nil {
						n.Requires = meta.Requires
					}
				}
			}
		}
		n.Requires, _ = diagnosticRequirements(s.Source, n.Requires)
		for _, text := range n.Requires {
			r, _ := dependency.ParseRef(text)
			children[name] = append(children[name], r)
		}
		nodes = append(nodes, n)
	}
	graph := dependency.AnalyzeGraph(nodes)
	shown := make(map[string]bool)
	var write func(string, string, string, string)
	write = func(name, prefix, branch, requested string) {
		s := m.Skills[name]
		if s == nil {
			fmt.Fprintf(out, "%s%s%s (missing entry)\n", prefix, branch, requested)
			return
		}
		line := fmt.Sprintf("%s/%s@%s", s.Source, name, labels[name])
		if requested != "" {
			r, _ := dependency.ParseRef(requested)
			line += " (requires " + r.Constraint + ")"
		} else if s.Version != "" {
			line += " (requested " + s.Version + ")"
		}
		if s.IsDirect() && len(graph.Via[name]) > 0 {
			line += " [direct]"
		}
		if shown[name] {
			fmt.Fprintf(out, "%s%s%s [already shown]\n", prefix, branch, line)
			return
		}
		fmt.Fprintf(out, "%s%s%s\n", prefix, branch, line)
		shown[name] = true
		if branch == "|-- " {
			prefix += "|   "
		} else if branch != "" {
			prefix += "    "
		}
		for i, r := range children[name] {
			branch := "|-- "
			if i == len(children[name])-1 {
				branch = "`-- "
			}
			write(r.Name, prefix, branch, r.String())
		}
	}
	for _, name := range names {
		if m.Skills[name].IsDirect() {
			write(name, "  ", "", "")
		}
	}
	if len(graph.Orphans) > 0 {
		fmt.Fprintln(out, "Orphaned transitive skills:")
		// Start detached subtrees at their roots, even when a child sorts first.
		for _, name := range graph.Orphans {
			if len(graph.Via[name]) == 0 {
				write(name, "  ", "", "")
			}
		}
		for _, name := range graph.Orphans {
			if !shown[name] { // also covers detached cycles
				write(name, "  ", "", "")
			}
		}
	}
	return nil
}

// diagnosticRequirements shares reference normalization between list and doctor.
// Keep valid edges even when a hand-edited snapshot contains malformed entries.
func diagnosticRequirements(alias string, raw []string) ([]string, []error) {
	var refs []dependency.Ref
	var problems []error
	for i, text := range raw {
		r, err := dependency.ParseRef(text)
		if err != nil {
			problems = append(problems, fmt.Errorf("requires[%d]: %w", i, err))
			continue
		}
		refs = append(refs, r)
	}
	var normalized []string
	for _, group := range dependency.GroupRefs(refs, alias) {
		for _, r := range group {
			normalized = append(normalized, r.String())
		}
	}
	slices.Sort(normalized)
	return normalized, problems
}

func writeInstalled(out io.Writer, skillsDir string, skills map[string]*manifest.Skill, names []string) error {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	for _, name := range names {
		sk := skills[name]
		if sk == nil {
			return fmt.Errorf("skill %s: nil manifest entry", name)
		}
		label, err := installedLabel(skillsDir, name, sk)
		if err != nil {
			return err
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", name, label, sk.Source)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write list: %w", err)
	}
	return nil
}

func installedLabel(skillsDir, name string, sk *manifest.Skill) (string, error) {
	if sk.Resolved == nil {
		return "not installed", nil
	}
	missing, err := skillMissing(skillsDir, name)
	if err != nil {
		return "", err
	}
	if missing {
		return "not installed", nil
	}
	return versionLabel(sk.Resolved.Version, sk.Resolved.Commit), nil
}

func skillMissing(skillsDir, name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(skillsDir, name))
	if err == nil {
		return false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, fmt.Errorf("stat skill: %w", err)
}

func writeAvailable(out io.Writer, m *manifest.Manifest, alias string) error {
	url, ok := m.Sources[alias]
	if !ok {
		return fmt.Errorf("unknown source %s", alias)
	}
	src, err := openFetch(alias, url)
	if err != nil {
		fmt.Fprintf(out, "Available in %s: unreachable (%v)\n", alias, err)
		return nil
	}
	fmt.Fprintf(out, "Available in %s:\n", alias)
	names, err := src.Skills()
	if err != nil {
		return fmt.Errorf("list source %s: %w", alias, err)
	}
	if len(names) == 0 {
		fmt.Fprintln(out, "  (none)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	for _, name := range names {
		versions, err := src.Versions(name)
		if err != nil {
			return fmt.Errorf("list source %s: %w", alias, err)
		}
		latest := "no release"
		if len(versions) > 0 {
			latest = versions[len(versions)-1]
		}
		mark := ""
		if sk := m.Skills[name]; sk != nil && sk.Source == alias {
			mark = "installed"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", name, latest, mark)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write list: %w", err)
	}
	return nil
}
