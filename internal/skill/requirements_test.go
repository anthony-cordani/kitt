package skill_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/testutil"
	"go.yaml.in/yaml/v3"
)

func TestParseFileRequirements(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		version  string
		want     []string
	}{
		{name: "no_metadata"},
		{name: "absent", metadata: "metadata:\n  version: 1.2.3\n  author: Ada\n", version: "1.2.3"},
		{name: "empty", metadata: "metadata:\n  requires: []\n", want: []string{}},
		{name: "block", metadata: "metadata:\n  version: 1.2.3\n  author: Ada\n  requires:\n    - code-review@^1\n    - other/us-review@1.2.3\n", version: "1.2.3", want: []string{"code-review@^1", "other/us-review@1.2.3"}},
		{name: "unquoted_scalar", metadata: "metadata:\n  requires: review@^1\n", want: []string{"review@^1"}},
		{name: "string", metadata: "metadata:\n  requires: \"code-review@^1.2 other/us-review@^0.2.3\"\n", want: []string{"code-review@^1.2", "other/us-review@^0.2.3"}},
		{name: "empty_string", metadata: "metadata:\n  requires: \"\"\n", want: []string{}},
		{name: "blank_string", metadata: "metadata:\n  requires: \" \\t \"\n", want: []string{}},
		{name: "multiline_string", metadata: "metadata:\n  requires: |-\n    code-review@^1\n    other/us-review@1.2.3\tadr@1.2.3\n", want: []string{"code-review@^1", "other/us-review@1.2.3", "adr@1.2.3"}},
		{name: "string_alias", metadata: "requirements: &refs code-review@^1\nmetadata:\n  requires: *refs\n", want: []string{"code-review@^1"}},
		{name: "flow", metadata: "metadata:\n  requires: [\"code-review@^1.2\", \"other/us-review@^0.2.3\"]\n", want: []string{"code-review@^1.2", "other/us-review@^0.2.3"}},
		{name: "raw_strings", metadata: "metadata:\n  requires: [\"\", \"not a valid ref\", \"123\", \"true\", \"null\"]\n", want: []string{"", "not a valid ref", "123", "true", "null"}},
		{name: "other_scalar_metadata", metadata: "metadata:\n  version: 1.2\n  enabled: true\n  count: 2\n", version: "1.2"},
		{name: "other_complex_metadata", metadata: "metadata:\n  tags: [ai, review]\n  author: {name: Ada}\n"},
		{name: "aliases", metadata: "metadata:\n  first: &ref code-review@^1\n  requires: &refs [*ref]\n", want: []string{"code-review@^1"}},
		{name: "sequence_alias", metadata: "requirements: &refs [code-review@^1]\nmetadata:\n  requires: *refs\n", want: []string{"code-review@^1"}},
	}
	for _, tt := range tests {
		for endingName, ending := range map[string]string{"lf": "\n", "crlf": "\r\n"} {
			t.Run(tt.name+"_"+endingName, func(t *testing.T) {
				doc := "---\nname: review\ndescription: Read carefully.\n" + tt.metadata + "---\nBody.\n"
				meta := parseSkill(t, strings.ReplaceAll(doc, "\n", ending))
				if meta.Name != "review" || meta.Description != "Read carefully." || meta.Version != tt.version || !reflect.DeepEqual(meta.Requires, tt.want) {
					t.Fatalf("ParseFile = %+v, want version %q and requirements %#v", meta, tt.version, tt.want)
				}
			})
		}
	}
}

func TestParseFileUnquotedRequirementStrings(t *testing.T) {
	for endingName, ending := range map[string]string{"lf": "\n", "crlf": "\r\n"} {
		t.Run(endingName, func(t *testing.T) {
			const doc = "---\nname: review\ndescription: Review code.\nmetadata:\n  requires:\n    - code-review@^1.0.0\n    - team/review@^1\n---\n"
			meta := parseSkill(t, strings.ReplaceAll(doc, "\n", ending))
			want := []string{"code-review@^1.0.0", "team/review@^1"}
			if !reflect.DeepEqual(meta.Requires, want) {
				t.Fatalf("unquoted requirements = %#v, want %#v", meta.Requires, want)
			}
		})
	}
}

func TestForeignRequires(t *testing.T) {
	tests := []struct {
		name string
		meta string
		want bool
	}{
		{name: "absent", meta: "metadata:\n  version: \"1.0.0\"\n", want: false},
		{name: "string", meta: "metadata:\n  requires: \"python3\"\n", want: true},
		{name: "foreign_words", meta: "metadata:\n  requires: \"numpy pandas\"\n", want: true},
		{name: "empty_string", meta: "metadata:\n  requires: \"\"\n", want: true},
		{name: "kitt_string", meta: "metadata:\n  requires: \"leaf@^1 other/child@1.0.0\"\n", want: false},
		{name: "invalid_kitt_string", meta: "metadata:\n  requires: \"code-review@^1 db@@1\"\n", want: false},
		{name: "mixed_kitt_string", meta: "metadata:\n  requires: \"numpy leaf@^1\"\n", want: false},
		{name: "string_alias", meta: "requirements: &refs leaf@^1\nmetadata:\n  requires: *refs\n", want: false},
		{name: "list", meta: "metadata:\n  requires: [\"numpy\"]\n", want: true},
		{name: "list without at", meta: "metadata:\n  requires:\n    - numpy\n    - python3\n", want: true},
		{name: "empty list", meta: "metadata:\n  requires: []\n", want: false},
		{name: "number", meta: "metadata:\n  requires: [123]\n", want: true},
		{name: "mapping", meta: "metadata:\n  requires: {name: review}\n", want: true},
		{name: "kitt", meta: "metadata:\n  requires: [\"leaf@^1\"]\n", want: false},
		{name: "mixed number after", meta: "metadata:\n  requires: [\"leaf@^1\", 123]\n", want: false},
		{name: "mixed number before", meta: "metadata:\n  requires: [123, \"leaf@^1\"]\n", want: false},
		{name: "mixed mapping", meta: "metadata:\n  requires: [{name: leaf}, \"leaf@^1\"]\n", want: false},
		{name: "double at", meta: "metadata:\n  requires: [\"db@@1.0\"]\n", want: false},
		{name: "empty constraint", meta: "metadata:\n  requires: [\"db@\"]\n", want: false},
		{name: "bad alias", meta: "metadata:\n  requires: [\"bad alias/name@^1\"]\n", want: false},
		{name: "mixed", meta: "metadata:\n  requires: [\"numpy\", \"db@@1.0\"]\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "SKILL.md")
			doc := "---\nname: review\ndescription: Read carefully.\n" + tt.meta + "---\n"
			testutil.WriteFile(t, path, doc)
			got, err := skill.ForeignRequires(path)
			if err != nil || got != tt.want {
				t.Fatalf("ForeignRequires = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestParseFileRejectsNonStringVersion(t *testing.T) {
	for endingName, ending := range map[string]string{"lf": "\n", "crlf": "\r\n"} {
		t.Run(endingName, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "SKILL.md")
			doc := "---\nname: review\ndescription: Read carefully.\nmetadata:\n  version: [1, 2]\n---\n"
			testutil.WriteFile(t, path, strings.ReplaceAll(doc, "\n", ending))
			_, err := skill.ParseFile(path)
			want := fmt.Sprintf("parse skill frontmatter %q: metadata.version at line 5: must be a string", path)
			if err == nil || err.Error() != want {
				t.Fatalf("ParseFile error = %v, want %q", err, want)
			}
		})
	}
}

func TestParseFileRejectsMalformedRequirements(t *testing.T) {
	tests := []struct {
		name  string
		value string
		index int
		line  int
		kind  string
	}{
		{name: "mapping", value: "{name: review}", line: 5, kind: "string or sequence of strings"},
		{name: "null", value: "null", line: 5, kind: "string or sequence of strings"},
		{name: "implicit_null", value: "", line: 5, kind: "string or sequence of strings"},
		{name: "number", value: "[review@^1, 123]", index: 1, line: 5, kind: "string"},
		{name: "boolean", value: "[true]", line: 5, kind: "string"},
		{name: "null_element", value: "[null]", line: 5, kind: "string"},
		{name: "map_element", value: "[{name: review}]", line: 5, kind: "string"},
		{name: "sequence_element", value: "[[]]", line: 5, kind: "string"},
		{name: "block_index", value: "\n    - review@^1\n    - 3\n", index: 1, line: 7, kind: "string"},
		{name: "tagged_number", value: "[!!int 123]", line: 5, kind: "string"},
	}
	for _, tt := range tests {
		for endingName, ending := range map[string]string{"lf": "\n", "crlf": "\r\n"} {
			t.Run(tt.name+"_"+endingName, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "SKILL.md")
				doc := "---\nname: review\ndescription: Read carefully.\nmetadata:\n  requires: " + tt.value + "\n---\n"
				testutil.WriteFile(t, path, strings.ReplaceAll(doc, "\n", ending))
				_, err := skill.ParseFile(path)
				want := fmt.Sprintf("parse skill frontmatter %q: metadata.requires", path)
				if tt.kind == "string or sequence of strings" {
					want += fmt.Sprintf(" at line %d: must be a string or sequence of strings", tt.line)
				} else {
					want = fmt.Sprintf("parse skill frontmatter %q: metadata.requires[%d] at line %d: must be a string", path, tt.index, tt.line)
				}
				if err == nil || err.Error() != want {
					t.Fatalf("ParseFile error = %v, want %q", err, want)
				}
			})
		}
	}
}

const publishedDependencySkill = `---
name: us-deliver
description: Deliver a user story.
metadata:
  version: "1.0.0"
  requires: "code-review@^1.0.0 us-review@^1.0.0"
---
`

func TestParsePublishedStringRequirements(t *testing.T) {
	meta := parseSkill(t, publishedDependencySkill)
	want := []string{"code-review@^1.0.0", "us-review@^1.0.0"}
	if meta.Name != "us-deliver" || meta.Description != "Deliver a user story." || meta.Version != "1.0.0" || !reflect.DeepEqual(meta.Requires, want) {
		t.Fatalf("published skill = %+v, want requirements %v", meta, want)
	}
}

func TestStringRequirementsLegacyMetadataCompatibility(t *testing.T) {
	var legacy struct {
		Name        string            `yaml:"name"`
		Description string            `yaml:"description"`
		Metadata    map[string]string `yaml:"metadata"`
	}
	raw, _, _ := strings.Cut(strings.TrimPrefix(publishedDependencySkill, "---\n"), "\n---")
	if err := yaml.Unmarshal([]byte(raw), &legacy); err != nil {
		t.Fatalf("0.1.x metadata decoding: %v", err)
	}
	if legacy.Name != "us-deliver" || legacy.Description != "Deliver a user story." || legacy.Metadata["version"] != "1.0.0" || legacy.Metadata["requires"] != "code-review@^1.0.0 us-review@^1.0.0" {
		t.Fatalf("legacy metadata = %+v", legacy)
	}
}
