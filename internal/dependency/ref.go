// Package dependency parses skill requirements without resolving or installing them.
package dependency

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

var sourceAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Ref identifies a skill requirement, optionally qualified by a source alias.
type Ref struct {
	Source     string
	Name       string
	Constraint string
}

// ParseRef accepts name@constraint or alias/name@constraint.
func ParseRef(s string) (Ref, error) {
	if strings.Contains(s, `\`) {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: backslashes are not allowed", s)
	}
	if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: whitespace and control characters are not allowed", s)
	}
	name, constraint, ok := strings.Cut(s, "@")
	if !ok {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: missing @constraint", s)
	}
	if constraint == "" {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: constraint must not be empty", s)
	}
	if strings.Contains(constraint, "@") {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: must contain exactly one @", s)
	}
	ref, err := parseName(name)
	if err != nil {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: %w", s, err)
	}
	if err := source.ValidateConstraint(constraint); err != nil {
		return Ref{}, fmt.Errorf("invalid dependency reference %q: %w", s, err)
	}
	ref.Constraint = constraint
	return ref, nil
}

func parseName(s string) (Ref, error) {
	if strings.Count(s, "/") > 1 {
		return Ref{}, fmt.Errorf("expected name or alias/name, not a path")
	}
	ref := Ref{Name: s}
	if alias, name, ok := strings.Cut(s, "/"); ok {
		if alias == "" {
			return Ref{}, fmt.Errorf("source alias must not be empty")
		}
		if !sourceAliasPattern.MatchString(alias) {
			return Ref{}, fmt.Errorf("invalid source alias %q: must be a source alias, not a path or alias=url", alias)
		}
		ref.Source, ref.Name = alias, name
	}
	if ref.Name == "" {
		return Ref{}, fmt.Errorf("skill name must not be empty")
	}
	if err := skill.ValidName(ref.Name); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// String returns the reference with its current source qualification.
func (r Ref) String() string {
	name := r.Name
	if r.Source != "" {
		name = r.Source + "/" + name
	}
	return name + "@" + r.Constraint
}

// WithDefaultSource qualifies a bare reference with its parent's valid source alias.
func (r Ref) WithDefaultSource(parentAlias string) Ref {
	if r.Source == "" {
		r.Source = parentAlias
	}
	return r
}

// GroupRefs normalizes sources and deduplicates identical requirements, grouping
// differing constraints by source and name. Groups and refs retain first-seen order.
func GroupRefs(refs []Ref, parentAlias string) [][]Ref {
	var groups [][]Ref
	bySkill := make(map[Ref]int)
	seen := make(map[Ref]bool)
	for _, ref := range refs {
		ref = ref.WithDefaultSource(parentAlias)
		if seen[ref] {
			continue
		}
		seen[ref] = true
		key := Ref{Source: ref.Source, Name: ref.Name}
		i, ok := bySkill[key]
		if !ok {
			i = len(groups)
			bySkill[key] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], ref)
	}
	return groups
}

// NormalizeRequirements parses, qualifies, sorts and deduplicates requirements.
// The returned slice is nonnil, including for a leaf. For a saved snapshot,
// inherited aliases may already have been qualified by kitt; only frontmatter
// aliases explicitly written by authors are restricted to the alias whitelist.
func NormalizeRequirements(raw []string, parentAlias string, snapshot bool) ([]string, error) {
	out := make([]string, 0, len(raw))
	for i, text := range raw {
		if snapshot && strings.HasPrefix(text, parentAlias+"/") {
			text = strings.TrimPrefix(text, parentAlias+"/")
		}
		ref, err := ParseRef(text)
		if err != nil {
			return nil, &ValidationError{Location: fmt.Sprintf("metadata.requires[%d]", i), Err: err}
		}
		out = append(out, ref.WithDefaultSource(parentAlias).String())
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// ParseSnapshotRef reads a previously normalized requirement. Its source alias
// may have been inherited from a legacy manifest, rather than authored in YAML.
// Frontmatter must always use ParseRef instead.
func ParseSnapshotRef(text string) (Ref, error) {
	alias, rest, qualified := strings.Cut(text, "/")
	if !qualified {
		return ParseRef(text)
	}
	ref, err := ParseRef(rest)
	if err != nil {
		return Ref{}, err
	}
	ref.Source = alias
	return ref, nil
}
