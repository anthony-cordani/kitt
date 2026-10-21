package release

import (
	"fmt"
	"strings"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/source"
)

// releaseSources checks immediate requirements only. Enumerating publications
// does not inspect their trees: the planner first selects matching candidates.
type releaseSources struct {
	root     string
	urls     map[string]string
	opened   map[string]*source.Source
	versions map[string]string
}

func (s *releaseSources) Releases(alias, name string) ([]dependency.Release, error) {
	key := alias + "/" + name
	out, err := runGit(s.root, "ls-remote", "--tags", "--", s.urls[key], "refs/tags/"+name+"/v*")
	if err != nil {
		return nil, &dependency.AccessError{Source: alias, Name: name, Operation: "list published releases", Err: err}
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			refs[fields[1]] = fields[0]
		}
	}
	var releases []dependency.Release
	prefix := "refs/tags/" + name + "/v"
	for ref, object := range refs {
		version := strings.TrimPrefix(ref, prefix)
		if !strings.HasPrefix(ref, prefix) || !plainVersion(version) {
			continue
		}
		if commit, ok := refs[ref+"^{}"]; ok {
			object = commit
		}
		releases = append(releases, dependency.Release{Version: version, Commit: object})
		s.versions[key+"@"+object] = version
	}
	return releases, nil
}

func (s *releaseSources) Requires(alias, name, commit string) ([]string, error) {
	key := alias + "/" + name
	url := s.urls[key]
	src := s.opened[url]
	if src == nil {
		var err error
		src, err = source.Open(alias, url)
		if err != nil {
			return nil, err
		}
		if err := src.Fetch(); err != nil {
			return nil, err
		}
		s.opened[url] = src
	}
	version := s.versions[key+"@"+commit]
	pin, err := src.Resolve(name, version)
	if err != nil {
		// A missing skill invalidates this candidate, allowing another matching
		// publication to satisfy the requirement. Preserve source error context.
		return nil, &dependency.ValidationError{Location: key + "@" + version, Err: &dependency.AccessError{
			Source: alias, Name: name, Commit: commit, Operation: "check skill presence", Err: err,
		}}
	}
	if pin.Commit != commit {
		return nil, fmt.Errorf("published tag %q changed; refresh source %q", name+"/v"+version, alias)
	}
	return nil, nil
}
