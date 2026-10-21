package skill_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/testutil"
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
		{name: "flow", metadata: "metadata:\n  requires: [\"code-review@^1.2\", \"other/us-review@^0.2.3\"]\n", want: []string{"code-review@^1.2", "other/us-review@^0.2.3"}},
		{name: "raw_strings", metadata: "metadata:\n  requires: [\"\", \"not a valid ref\", \"123\", \"true\", \"null\"]\n", want: []string{"", "not a valid ref", "123", "true", "null"}},
		{name: "other_scalar_metadata", metadata: "metadata:\n  version: 1.2\n  enabled: true\n  count: 2\n", version: "1.2"},
		{name: "other_complex_metadata", metadata: "metadata:\n  tags: [ai, review]\n  author: {name: Ada}\n"},
		{name: "aliases", metadata: "metadata:\n  first: &ref code-review@^1\n  requires: &refs [*ref]\n", want: []string{"code-review@^1"}},
		{name: "sequence_alias", metadata: "requirements: &refs [code-review@^1]\nmetadata:\n  requires: *refs\n", want: []string{"code-review@^1"}},
	}
	for _, tt := range tests {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(tt.name+fmt.Sprintf("_%q", ending), func(t *testing.T) {
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
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", ending), func(t *testing.T) {
			const doc = "---\nname: review\ndescription: Review code.\nmetadata:\n  requires:\n    - code-review@^1.0.0\n    - team/review@^1\n---\n"
			meta := parseSkill(t, strings.ReplaceAll(doc, "\n", ending))
			want := []string{"code-review@^1.0.0", "team/review@^1"}
			if !reflect.DeepEqual(meta.Requires, want) {
				t.Fatalf("unquoted requirements = %#v, want %#v", meta.Requires, want)
			}
		})
	}
}

func TestParseFileRejectsNonStringVersion(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", ending), func(t *testing.T) {
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
		{name: "scalar", value: "review@^1", line: 5, kind: "sequence of strings"},
		{name: "mapping", value: "{name: review}", line: 5, kind: "sequence of strings"},
		{name: "null", value: "null", line: 5, kind: "sequence of strings"},
		{name: "implicit_null", value: "", line: 5, kind: "sequence of strings"},
		{name: "number", value: "[review@^1, 123]", index: 1, line: 5, kind: "string"},
		{name: "boolean", value: "[true]", line: 5, kind: "string"},
		{name: "null_element", value: "[null]", line: 5, kind: "string"},
		{name: "map_element", value: "[{name: review}]", line: 5, kind: "string"},
		{name: "sequence_element", value: "[[]]", line: 5, kind: "string"},
		{name: "block_index", value: "\n    - review@^1\n    - 3\n", index: 1, line: 7, kind: "string"},
		{name: "tagged_number", value: "[!!int 123]", line: 5, kind: "string"},
	}
	for _, tt := range tests {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(tt.name+fmt.Sprintf("_%q", ending), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "SKILL.md")
				doc := "---\nname: review\ndescription: Read carefully.\nmetadata:\n  requires: " + tt.value + "\n---\n"
				testutil.WriteFile(t, path, strings.ReplaceAll(doc, "\n", ending))
				_, err := skill.ParseFile(path)
				want := fmt.Sprintf("parse skill frontmatter %q: metadata.requires", path)
				if tt.kind == "sequence of strings" {
					want += fmt.Sprintf(" at line %d: must be a sequence of strings", tt.line)
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
