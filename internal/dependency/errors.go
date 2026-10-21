package dependency

import (
	"fmt"
	"slices"
	"strings"
)

// Scope supplies diagnostic context; zero values mean project scope and install.
// ManifestPath should be the actual selected project or global manifest path.
type Scope struct {
	Global       bool
	ManifestPath string
	Command      string
}

func (s Scope) label() string {
	if s.Global {
		return "global"
	}
	return "project"
}

func (s Scope) prefix() string {
	command := s.Command
	if command == "" {
		command = "install"
	}
	return "kitt " + command + ": "
}

// Requirement records an incoming edge or an explicit manifest/target constraint.
// Index is the original zero-based metadata.requires index (-1 for manifest/target).
type Requirement struct {
	Ref       Ref
	Requester string
	Source    string
	Version   string
	Commit    string
	Index     int
	Manifest  bool
	Target    bool
}

func (r Requirement) owner() string {
	v := r.Version
	if v == "" {
		v = r.Commit
	}
	name := r.Source + "/" + r.Requester
	if v != "" {
		name += "@" + v
	}
	return name
}

func requestLines(requests []Requirement) string {
	var b strings.Builder
	counts := make(map[string]int)
	for _, r := range requests {
		if !r.Manifest && !r.Target {
			counts[r.owner()]++
		}
	}
	for _, r := range sortedRequests(requests) {
		if r.Manifest || r.Target {
			origin := "manifest"
			if r.Target {
				origin = "requested target"
			}
			ref := r.Ref.String()
			if r.Ref.Constraint == "" {
				ref = r.Ref.Source + "/" + r.Ref.Name
			}
			fmt.Fprintf(&b, "\n  %s requires %q (%s)", r.owner(), ref, origin)
		} else {
			fmt.Fprintf(&b, "\n  %s requires %s", r.owner(), r.Ref.Constraint)
			if counts[r.owner()] > 1 {
				fmt.Fprintf(&b, " (metadata.requires[%d])", r.Index)
			}
		}
	}
	return b.String()
}

func sortedRequests(in []Requirement) []Requirement {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b Requirement) int {
		if c := strings.Compare(a.owner(), b.owner()); c != 0 {
			return c
		}
		if c := strings.Compare(a.Ref.String(), b.Ref.String()); c != 0 {
			return c
		}
		return a.Index - b.Index
	})
	return out
}

// ConflictReason distinguishes logical incompatibility from missing publications.
type ConflictReason string

const (
	EmptyIntersection ConflictReason = "empty intersection"
	NoMatchingRelease ConflictReason = "ranges overlap, but no published release matches"
	SourceCollision   ConflictReason = "same skill name requested from different sources"
)

// ConflictError includes every currently selected requester, including manifest constraints.
type ConflictError struct {
	Scope    Scope
	Name     string
	Sources  []string
	Requests []Requirement
	Reason   ConflictReason
}

func (e *ConflictError) Error() string {
	sources := slices.Clone(e.Sources)
	slices.Sort(sources)
	name := e.Name
	if len(sources) > 0 {
		name = sources[0] + "/" + name
	}
	text := e.Scope.prefix() + "incompatible requirements for " + name
	if e.Reason == SourceCollision {
		quoted := make([]string, len(sources))
		for i, alias := range sources {
			quoted[i] = fmt.Sprintf("%q", alias+"/"+e.Name)
		}
		text += "\n  conflicting sources: " + strings.Join(quoted, ", ")
	} else if e.Reason == NoMatchingRelease {
		text += "\n  " + string(e.Reason)
	}
	lines := requestLines(e.Requests)
	if e.Reason == SourceCollision {
		lines = ""
		for _, r := range sortedRequests(e.Requests) {
			ref := r.Ref.String()
			if r.Ref.Constraint == "" {
				ref = r.Ref.Source + "/" + r.Ref.Name
			}
			lines += fmt.Sprintf("\n  %s requires %q", r.owner(), ref)
		}
	}
	return text + lines + "\n  one version per skill name is allowed in " + e.Scope.label() + " scope"
}

// UnknownSourceError identifies the referring requirement and alias recovery.
type UnknownSourceError struct {
	Scope       Scope
	Alias       string
	Requirement Requirement
}

func (e *UnknownSourceError) Error() string {
	path := e.Scope.ManifestPath
	if path == "" {
		path = "kitt.toml"
	}
	text := e.Scope.prefix() + fmt.Sprintf("unknown source alias %q", e.Alias)
	if e.Requirement.Requester != "" {
		text += fmt.Sprintf("\n  required by %s: %q", e.Requirement.owner(), e.Requirement.Ref.String())
	}
	text += fmt.Sprintf("\n  missing entry: [sources].%s in %s", e.Alias, path)
	command := "kitt init --source"
	if e.Scope.Global {
		command = "kitt install -g --source"
	}
	return text + fmt.Sprintf("\n  add it with: %s %q", command, e.Alias+"=<repository-url>")
}

// NoReleasesError rejects branch tips as evidence for a dependency range.
type NoReleasesError struct {
	Scope    Scope
	Source   string
	Name     string
	Requests []Requirement
}

func (e *NoReleasesError) Error() string {
	requests := sortedRequests(e.Requests)
	var lines []string
	for _, r := range requests {
		lines = append(lines, r.owner()+" requires "+r.Ref.String())
	}
	if len(lines) == 0 {
		lines = append(lines, e.Source+"/"+e.Name)
	}
	return strings.Join(lines, "\n") + "\n  " + e.Name + " has no published releases; a branch-tip pin cannot satisfy this range"
}

// CycleError contains the complete closed cycle, including a repeated first node.
type CycleError struct{ Path []string }

func (e *CycleError) Error() string { return "dependency cycle: " + strings.Join(e.Path, " -> ") }

// UpgradeRefusalError lists all selected dependents blocking an explicit candidate.
// It also protects explicit install candidates; Operation then equals "install".
type UpgradeRefusalError struct {
	Source    string
	Name      string
	From      string
	To        string
	Operation CandidatePolicy
	Blocking  []Requirement
}

func (e *UpgradeRefusalError) Error() string {
	operation := "upgrade"
	if e.Operation == Install {
		operation = "install"
	}
	return fmt.Sprintf("cannot %s %s/%s from %s to %s", operation, e.Source, e.Name, e.From, e.To) + requestLines(e.Blocking)
}

// ValidationError preserves parse/metadata context separately from graph conflicts.
type ValidationError struct {
	Location string
	Err      error
}

func (e *ValidationError) Error() string { return e.Location + ": " + e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// AccessError preserves source failures so callers can distinguish Git access
// errors from an unsatisfiable graph using errors.As/Is.
type AccessError struct {
	Source, Name, Commit, Operation string
	Err                             error
}

func (e *AccessError) Error() string {
	return fmt.Sprintf("%s %s/%s at %q: %v", e.Operation, e.Source, e.Name, e.Commit, e.Err)
}
func (e *AccessError) Unwrap() error { return e.Err }

// PlanError wraps every BuildPlan failure. ManagedStateUntouched describes only
// the planner: callers must separately account for their own preceding writes.
// Error text deliberately leaves any "no changes applied" claim to the caller.
type PlanError struct {
	Err                   error
	ManagedStateUntouched bool
}

func (e *PlanError) Error() string { return e.Err.Error() }
func (e *PlanError) Unwrap() error { return e.Err }
