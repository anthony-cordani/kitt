package install

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
	"golang.org/x/mod/semver"
)

// Upgrade plans all requested releases together before applying any changes.
func Upgrade(opts Options, name string, major bool) (err error) {
	applying := false
	defer func() {
		if err != nil {
			err = &dependency.PlanError{Err: err, ManagedStateUntouched: !applying}
		}
	}()
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
	names, err := upgradeTargets(m, p.manifest, name)
	if err != nil {
		return err
	}
	if err := prepareOwnership(&p, m); err != nil {
		return err
	}
	access := &installSources{git: dependency.NewGitSources(m.Sources), current: m, opened: map[string]*source.Source{}, paths: p}
	current, _, err := planningManifest(p, m, access)
	if err != nil {
		return err
	}
	targets, notes, err := upgradePlanTargets(current, names, major, access)
	if err != nil {
		return err
	}
	plan, err := dependency.BuildPlan(dependency.Scope{Global: p.global, ManifestPath: p.manifest, Command: "upgrade"}, current, targets, access)
	if err != nil {
		return err
	}
	// Preserve the established caret-floor update for direct upgrade targets.
	// Transitive entries keep their own (normally absent) manifest constraint.
	for i := range plan.Nodes {
		n := &plan.Nodes[i]
		old := m.Skills[n.Name]
		if n.Direct && slices.Contains(names, n.Name) && !exactPin(old.Version) {
			n.Constraint = old.Version
			if n.Version != "" && (old.Resolved == nil || old.Resolved.Commit != n.Commit) {
				n.Constraint = "^" + n.Version
			}
		}
	}
	// Prepare diffs before application as Git failures must leave state untouched.
	var report bytes.Buffer
	if err := reportUpgrade(&report, current, plan, names, notes, access); err != nil {
		return err
	}
	out := outputOf(opts.Out)
	_, applying, err = stageAndApplyPlan(p, m, plan, access, true, false, out)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, &report)
	return err
}

func upgradeTargets(m *manifest.Manifest, manifestPath, name string) ([]string, error) {
	if name == "" {
		var names []string
		for _, n := range sortedNames(m.Skills) {
			if m.Skills[n] != nil && m.Skills[n].IsDirect() {
				names = append(names, n)
			}
		}
		return names, nil
	}
	if _, ok := m.Skills[name]; !ok {
		return nil, fmt.Errorf("skill %s is not in %s", name, manifestPath)
	}
	return []string{name}, nil
}

func upgradePlanTargets(m *manifest.Manifest, names []string, major bool, access *installSources) ([]dependency.Target, map[string]string, error) {
	var targets []dependency.Target
	notes := map[string]string{}
	for _, name := range names {
		s := m.Skills[name]
		if exactPin(s.Version) {
			continue
		}
		t := dependency.Target{Name: name, Source: s.Source, Policy: dependency.Upgrade, PreserveDirect: true, BoundByDependents: !s.IsDirect()}
		releases, err := access.Releases(s.Source, name)
		if err != nil {
			return nil, nil, err
		}
		latest := ""
		for _, r := range releases {
			if semver.Compare("v"+r.Version, "v"+latest) > 0 {
				latest = r.Version
			}
		}
		if major && s.IsDirect() && latest != "" {
			// --major may replace the target's own range, never an incoming edge.
			t.Constraint = "^" + latest
		} else if s.IsDirect() && s.Version == "" && s.Resolved != nil && s.Resolved.Version != "" {
			t.Constraint = "^" + s.Resolved.Version
		}
		constraint := t.Constraint
		if constraint == "" {
			constraint = s.Version
		}
		if !major && latest != "" && constraint != "" {
			allowed, _ := source.Satisfies(constraint, latest)
			currentMajor, _ := versionMajor(constraint)
			latestMajor, _ := versionMajor(latest)
			if !allowed && latestMajor > currentMajor {
				notes[name] = fmt.Sprintf("note: %s %s is available (major upgrade, use --major)", name, latest)
			}
		}
		targets = append(targets, t)
	}
	return targets, notes, nil
}

func reportUpgrade(out io.Writer, current *manifest.Manifest, plan *dependency.Plan, names []string, notes map[string]string, access *installSources) error {
	for _, n := range plan.Nodes {
		old := current.Skills[n.Name]
		requested := slices.Contains(names, n.Name)
		if requested && exactPin(old.Version) {
			fmt.Fprintf(out, "kept %s %s (exact pin in kitt.toml)\n", n.Name, old.Version)
			continue
		}
		if note := notes[n.Name]; note != "" {
			fmt.Fprintln(out, note)
		}
		if old != nil && old.Resolved != nil && old.Resolved.Commit == n.Commit {
			if requested {
				fmt.Fprintf(out, "up to date %s %s\n", n.Name, versionLabel(n.Version, n.Commit))
			}
		} else {
			from := "-"
			if old != nil && old.Resolved != nil {
				from = versionLabel(old.Resolved.Version, old.Resolved.Commit)
				src, err := access.exact(n.Source, n.Commit)
				if err != nil {
					return err
				}
				diff, err := src.Diff(n.Name, old.Resolved.Commit, n.Commit)
				if err != nil {
					return fmt.Errorf("diff skill %s: %w", n.Name, err)
				}
				fmt.Fprint(out, diff)
			}
			if old == nil {
				fmt.Fprintf(out, "added dependency %s %s\n", n.Name, versionLabel(n.Version, n.Commit))
			} else {
				fmt.Fprintf(out, "upgraded %s %s -> %s\n", n.Name, from, versionLabel(n.Version, n.Commit))
			}
		}
		var before []string
		if old != nil && old.Resolved != nil && old.Resolved.Requires != nil {
			for _, text := range *old.Resolved.Requires {
				r, _ := dependency.ParseRef(text)
				before = append(before, r.WithDefaultSource(old.Source).String())
			}
		}
		slices.Sort(before)
		before = slices.Compact(before)
		for _, text := range before {
			if !slices.Contains(n.Requires, text) {
				fmt.Fprintf(out, "dependency removed %s -> %s\n", n.Name, text)
			}
		}
		for _, text := range n.Requires {
			if !slices.Contains(before, text) {
				fmt.Fprintf(out, "dependency added %s -> %s\n", n.Name, text)
			}
		}
	}
	for _, name := range plan.Orphans {
		fmt.Fprintf(out, "orphaned %s (retained)\n", name)
	}
	return nil
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
