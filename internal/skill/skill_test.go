package skill_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestValidName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{name: "empty", in: "", ok: false},
		{name: "len_65", in: strings.Repeat("a", 65), ok: false},
		{name: "uppercase", in: "Review", ok: false},
		{name: "leading_hyphen", in: "-review", ok: false},
		{name: "trailing_hyphen", in: "review-", ok: false},
		{name: "double_hyphen", in: "re--view", ok: false},
		{name: "underscore", in: "re_view", ok: false},
		{name: "space", in: "re view", ok: false},
		{name: "simple", in: "review", ok: true},
		{name: "hyphenated", in: "pdf-processing", ok: true},
		{name: "alnum", in: "a1", ok: true},
		{name: "len_64", in: strings.Repeat("a", 64), ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := skill.ValidName(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("ValidName(%q) = %v, want nil", tt.in, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("ValidName(%q) succeeded, want error", tt.in)
			}
		})
	}
}

func TestParseFileReadsFrontmatter(t *testing.T) {
	meta := parseSkill(t, frontmatterDoc())
	assertFrontmatter(t, meta)
}

func TestParseFileAcceptsCRLF(t *testing.T) {
	meta := parseSkill(t, strings.ReplaceAll(frontmatterDoc(), "\n", "\r\n"))
	assertFrontmatter(t, meta)
}

func TestParseFileRejectsMissingFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	testutil.WriteFile(t, path, "Just a body.\n")
	meta, err := skill.ParseFile(path)
	if err == nil {
		t.Fatalf("ParseFile returned %+v, want error", meta)
	}
}

func TestParseFileAbsentVersionIsEmpty(t *testing.T) {
	const doc = "---\nname: review\ndescription: Read code carefully.\n---\nBody.\n"
	meta := parseSkill(t, doc)
	if meta.Name != "review" || meta.Description != "Read code carefully." || meta.Version != "" {
		t.Fatalf("meta = %+v, want name review, description set, version empty", meta)
	}
}

func TestValidateRejectsNameMismatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "review")
	testutil.WriteFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: other\ndescription: A useful skill.\n---\n")
	meta, err := skill.Validate(dir)
	if err == nil {
		t.Fatalf("Validate returned %+v, want error", meta)
	}
	if !strings.Contains(err.Error(), "other") || !strings.Contains(err.Error(), "review") {
		t.Fatalf("Validate error = %q, want it to name the skill and the directory", err)
	}
}

func TestValidateRejectsEmptyDescription(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "review")
	testutil.WriteFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: review\ndescription: \"\"\n---\n")
	meta, err := skill.Validate(dir)
	if err == nil {
		t.Fatalf("Validate returned %+v, want error", meta)
	}
	if !strings.Contains(err.Error(), "description") {
		t.Fatalf("Validate error = %q, want it to mention the description", err)
	}
}

func TestValidateAcceptsMatchingName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "review")
	testutil.WriteFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: review\ndescription: A useful skill.\nmetadata:\n  version: \"1.0.0\"\n---\n")
	meta, err := skill.Validate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "review" || meta.Description != "A useful skill." || meta.Version != "1.0.0" {
		t.Fatalf("Validate = %+v", meta)
	}
}

func TestHashSameContentInTwoDirectories(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeTree(t, a, "alpha\n")
	writeTree(t, b, "alpha\n")
	ha := mustHash(t, a)
	hb := mustHash(t, b)
	if ha != hb {
		t.Fatalf("Hash = %s and %s, want equal", ha, hb)
	}
	if !strings.HasPrefix(ha, "h1:") {
		t.Fatalf("Hash = %q, want h1: prefix", ha)
	}
}

func TestHashChangesWhenOneByteChanges(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeTree(t, a, "alpha\n")
	writeTree(t, b, "alphb\n")
	if mustHash(t, a) == mustHash(t, b) {
		t.Fatal("Hash unchanged after a one-byte edit")
	}
}

func TestHashChangesWhenFileAdded(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeTree(t, a, "alpha\n")
	writeTree(t, b, "alpha\n")
	testutil.WriteFile(t, filepath.Join(b, "extra.txt"), "more\n")
	if mustHash(t, a) == mustHash(t, b) {
		t.Fatal("Hash unchanged after adding a file")
	}
}

func frontmatterDoc() string {
	return "---\nname: review\ndescription: Read code carefully.\nmetadata:\n  version: \"1.2.3\"\n---\nBody.\n"
}

func parseSkill(t *testing.T, content string) skill.Meta {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SKILL.md")
	testutil.WriteFile(t, path, content)
	meta, err := skill.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func assertFrontmatter(t *testing.T, got skill.Meta) {
	t.Helper()
	if got.Name != "review" || got.Description != "Read code carefully." || got.Version != "1.2.3" {
		t.Fatalf("meta = %+v, want name review, description %q, version 1.2.3", got, "Read code carefully.")
	}
}

func writeTree(t *testing.T, dir, body string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, "SKILL.md"), body)
	testutil.WriteFile(t, filepath.Join(dir, "references", "x.md"), "nested\n")
}

func mustHash(t *testing.T, dir string) string {
	t.Helper()
	sum, err := skill.Hash(dir)
	if err != nil {
		t.Fatalf("Hash(%s): %v", dir, err)
	}
	return sum
}
