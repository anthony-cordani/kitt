package install

import (
	"fmt"
	"strings"

	"github.com/anthony-cordani/kitt/internal/source"
)

// Show prints a skill's version, source, description, the version pinned here
// and its published versions. Nothing is written outside the source cache.
func Show(opts Options, ref string) error {
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
	t, err := installTarget(m, p, ref)
	if err != nil {
		return err
	}
	src, err := source.Open(t.Source, m.Sources[t.Source])
	if err != nil {
		return fmt.Errorf("open source %s: %w", t.Source, err)
	}
	out := outputOf(opts.Out)
	if err := src.Fetch(); err != nil {
		fmt.Fprintf(out, "warning: source %s unreachable, showing cached data: %v\n", t.Source, err)
	}
	versions, err := src.Versions(t.Name)
	if err != nil {
		return err
	}
	available := "none"
	if len(versions) > 0 {
		available = strings.Join(versions, ", ")
	}
	res, err := src.Resolve(t.Name, t.Constraint)
	if err != nil {
		if t.Constraint != "" {
			return fmt.Errorf("%w (available: %s)", err, available)
		}
		return err
	}
	meta, err := src.SkillMetadata(t.Name, res.Commit)
	if err != nil {
		return err
	}
	description := strings.TrimSpace(meta.Description)
	if description == "" {
		description = "(no description)"
	}
	fmt.Fprintf(out, "%s %s (%s)\n%s\n\n", t.Name, versionLabel(res.Version, res.Commit), t.Source, description)
	if s := m.Skills[t.Name]; s != nil && s.Source == t.Source && s.Resolved != nil {
		fmt.Fprintf(out, "Pinned here: %s\n", versionLabel(s.Resolved.Version, s.Resolved.Commit))
	}
	fmt.Fprintf(out, "Versions:    %s\n", available)
	return nil
}
