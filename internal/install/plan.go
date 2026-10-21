package install

import (
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

// installSources separates selecting releases from reading exact commits. Exact
// reads use the cache first, including when the original repository is gone.
type installSources struct {
	git     *dependency.GitSources
	current *manifest.Manifest
	restore bool
	opened  map[string]*source.Source
	paths   paths
}

func (a *installSources) Releases(alias, name string) ([]dependency.Release, error) {
	if s := a.current.Skills[name]; a.restore && s != nil && s.Resolved != nil {
		return nil, fmt.Errorf("restore cannot move pinned %s/%s@%s to satisfy edited requirements; use an explicit install or upgrade", alias, name, versionLabel(s.Resolved.Version, s.Resolved.Commit))
	}
	return a.git.Releases(alias, name)
}

func (a *installSources) Tip(alias, name string) (dependency.Release, error) {
	return a.git.Tip(alias, name)
}

func (a *installSources) exact(alias, commit string) (*source.Source, error) {
	s := a.opened[alias]
	if s == nil {
		url, ok := a.current.Sources[alias]
		if !ok {
			return nil, fmt.Errorf("unknown source %s", alias)
		}
		var err error
		s, err = source.Open(alias, url)
		if err != nil {
			return nil, err
		}
		a.opened[alias] = s
	}
	if !s.HasCommit(commit) {
		if err := s.Fetch(); err != nil {
			return nil, err
		}
		if !s.HasCommit(commit) {
			return nil, fmt.Errorf("commit %s not found in %s", shortCommit(commit), alias)
		}
	}
	return s, nil
}

func (a *installSources) Requires(alias, name, commit string) ([]string, error) {
	if err := preflightDestination(a.paths, name); err != nil {
		return nil, err
	}
	s, err := a.exact(alias, commit)
	if err != nil {
		return nil, err
	}
	return s.Requires(name, commit)
}

func planAndInstall(opts Options, ref *string) (err error) {
	applying := false
	defer func() {
		if err != nil {
			// Keep the original diagnostic and expose whether the caller may claim
			// that managed state was untouched. Cache writes do not count.
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
	var targets []dependency.Target
	if ref != nil {
		t, err := installTarget(m, p, *ref)
		if err != nil {
			return err
		}
		targets = []dependency.Target{t}
	}
	p.installed, err = readInstalledWithMode(p, m, true)
	if err != nil {
		return err
	}
	// Preserve existing ownership diagnostics, and collect adoption only in memory.
	for _, name := range sortedNames(m.Skills) {
		if err := p.installed.adopt(name, m.Skills[name]); err != nil {
			return err
		}
		if err := p.installed.check(name); err != nil {
			return err
		}
	}
	if len(targets) > 0 {
		if err := p.installed.check(targets[0].Name); err != nil {
			return err
		}
	}
	access := &installSources{git: dependency.NewGitSources(m.Sources), current: m, restore: ref == nil, opened: map[string]*source.Source{}, paths: p}
	current, aware, err := planningManifest(p, m, access)
	if err != nil {
		return err
	}
	plan, err := dependency.BuildPlan(dependency.Scope{Global: p.global, ManifestPath: p.manifest}, current, targets, access)
	if err != nil {
		return err
	}
	if err := preflightInstall(p, plan); err != nil {
		return err
	}
	staged, cleanup, err := stagePlan(p, m, plan, access)
	if err != nil {
		return err
	}
	defer cleanup()
	next := plannedManifest(m, plan, staged, aware || ref != nil)
	applying = true
	if err := applyStaged(p, plan, staged); err != nil {
		return err
	}
	out := outputOf(opts.Out)
	if err := finish(p, sortedNames(next.Skills), ref == nil, out); err != nil {
		return err
	}
	p.installed.deferred = false
	if p.installed.dirty {
		if err := p.installed.save(); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(m, next) {
		if err := next.Save(p.manifest); err != nil {
			return err
		}
	}
	for _, n := range plan.Nodes {
		verb := "ok"
		if len(targets) > 0 && n.Name == targets[0].Name {
			verb = "added"
		} else if s := m.Skills[n.Name]; s == nil || s.Resolved == nil {
			verb = "pinned"
		} else if staged[n.Name].dir != "" {
			verb = "installed"
		} else if ref != nil {
			continue
		}
		fmt.Fprintf(out, "%s %s %s\n", verb, n.Name, versionLabel(n.Version, n.Commit))
	}
	for _, name := range plan.Orphans {
		fmt.Fprintf(out, "orphaned %s (retained)\n", name)
	}
	return nil
}

func installTarget(m *manifest.Manifest, p paths, text string) (dependency.Target, error) {
	// ParseRef owns name/path validation; only CLI targets may omit a range.
	parsed := text
	if !strings.Contains(text, "@") {
		parsed += "@0.0.0"
	}
	r, err := dependency.ParseRef(parsed)
	if err != nil {
		return dependency.Target{}, err
	}
	if !strings.Contains(text, "@") {
		r.Constraint = ""
	}
	if strings.HasPrefix(r.Name, "kitt-") {
		return dependency.Target{}, fmt.Errorf("skill names starting with kitt- are reserved")
	}
	if r.Source == "" {
		if s := m.Skills[r.Name]; s != nil {
			r.Source = s.Source
		} else {
			switch len(m.Sources) {
			case 0:
				return dependency.Target{}, fmt.Errorf("no source in %s: add a [sources] table", p.manifest)
			case 1:
				for alias := range m.Sources {
					r.Source = alias
				}
			default:
				return dependency.Target{}, fmt.Errorf("several sources: use <source>/<skill>")
			}
		}
	}
	if _, ok := m.Sources[r.Source]; !ok {
		return dependency.Target{}, fmt.Errorf("unknown source %s", r.Source)
	}
	return dependency.Target{Name: r.Name, Source: r.Source, Constraint: r.Constraint, Policy: dependency.Install}, nil
}

// Populate legacy snapshots for planning, without necessarily persisting them.
// Only hash-verified installed content is authoritative. Otherwise read the pin.
func planningManifest(p paths, m *manifest.Manifest, access *installSources) (*manifest.Manifest, bool, error) {
	current := &manifest.Manifest{Sources: maps.Clone(m.Sources), Skills: map[string]*manifest.Skill{}}
	aware := false
	for _, name := range sortedNames(m.Skills) {
		s := m.Skills[name]
		if s == nil {
			return nil, false, fmt.Errorf("skill %s: nil manifest entry", name)
		}
		if err := source.ValidateConstraint(s.Version); err != nil {
			return nil, false, fmt.Errorf("skills.%s.version: %w", name, err)
		}
		if access.restore && s.Resolved != nil && s.Version != "" {
			ok, _ := source.Satisfies(s.Version, s.Resolved.Version)
			if !ok {
				return nil, false, fmt.Errorf("skills.%s.version = %q excludes stale pin %s; clear skills.%s.resolved (the resolved block) and run kitt install again", name, s.Version, versionLabel(s.Resolved.Version, s.Resolved.Commit), name)
			}
		}
		copy := *s
		current.Skills[name] = &copy
		aware = aware || s.Direct != nil || len(s.Via) > 0 || s.Resolved == nil
		if s.Resolved == nil {
			continue
		}
		pin := *s.Resolved
		copy.Resolved = &pin
		if pin.Requires != nil {
			aware = true
			continue
		}
		match, err := hashMatches(filepath.Join(p.skillsDir, name), pin.Hash)
		if err != nil {
			return nil, false, err
		}
		var raw []string
		if match {
			meta, err := skill.Validate(filepath.Join(p.skillsDir, name))
			if err != nil {
				return nil, false, err
			}
			raw = meta.Requires
		} else {
			raw, err = access.Requires(s.Source, name, pin.Commit)
			if err != nil {
				return nil, false, err
			}
		}
		if raw == nil {
			raw = []string{}
		}
		pin.Requires = &raw
		aware = aware || len(raw) > 0
	}
	if access.restore {
		for _, name := range sortedNames(current.Skills) {
			s := current.Skills[name]
			if s.Resolved == nil {
				continue
			}
			for _, text := range *s.Resolved.Requires {
				r, err := dependency.ParseRef(text)
				if err != nil {
					return nil, false, fmt.Errorf("skills.%s.resolved.requires: %w", name, err)
				}
				r = r.WithDefaultSource(s.Source)
				if _, known := m.Sources[r.Source]; known && current.Skills[r.Name] == nil {
					return nil, false, fmt.Errorf("incomplete manifest: pinned %s/%s requires %q, but skills.%s is missing; restore the missing manifest entry", s.Source, name, r.String(), r.Name)
				}
			}
		}
	}
	return current, aware, nil
}

func plannedManifest(m *manifest.Manifest, plan *dependency.Plan, staged map[string]stagedSkill, snapshots bool) *manifest.Manifest {
	next := &manifest.Manifest{Sources: maps.Clone(m.Sources), Skills: map[string]*manifest.Skill{}}
	for _, n := range plan.Nodes {
		s := &manifest.Skill{Source: n.Source, Version: n.Constraint, Via: slices.Clone(n.Via)}
		old := m.Skills[n.Name]
		if !n.Direct {
			s.Direct = new(false)
		} else if old != nil && old.Direct != nil && *old.Direct {
			s.Direct = new(true)
		}
		if len(s.Via) == 0 {
			s.Via = nil
		}
		if n.Direct && s.Version == "" && n.Version != "" && (old == nil || old.Resolved == nil) {
			s.Version = "^" + n.Version
		}
		s.Resolved = &manifest.Resolved{Version: n.Version, Commit: n.Commit, Hash: staged[n.Name].hash}
		if snapshots || old == nil || old.Resolved == nil || old.Resolved.Requires != nil {
			raw := slices.Clone(n.Requires)
			s.Resolved.Requires = &raw
		}
		next.Skills[n.Name] = s
	}
	return next
}
