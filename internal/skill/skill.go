// Package skill reads SKILL.md frontmatter and checks a skill directory.
package skill

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/sumdb/dirhash"
)

// Meta is the subset of SKILL.md frontmatter kitt reads.
type Meta struct {
	Name        string
	Description string
	Version     string // metadata.version, "" when absent
}

// ParseFile reads a SKILL.md file and returns its frontmatter.
func ParseFile(path string) (Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, fmt.Errorf("read skill file: %w", err)
	}
	raw, err := frontmatter(data)
	if err != nil {
		return Meta{}, fmt.Errorf("parse skill file: %w", err)
	}
	var doc struct {
		Name        string            `yaml:"name"`
		Description string            `yaml:"description"`
		Metadata    map[string]string `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Meta{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	meta := Meta{Name: doc.Name, Description: doc.Description}
	if doc.Metadata != nil {
		meta.Version = doc.Metadata["version"]
	}
	return meta, nil
}

// ValidName reports whether name follows the Agent Skills naming rules.
func ValidName(name string) error {
	count := 0
	for _, r := range name {
		count++
		if !isNameRune(r) {
			return fmt.Errorf("invalid skill name %q: must contain only a-z, 0-9, and hyphen", name)
		}
	}
	if count < 1 || count > 64 {
		return fmt.Errorf("invalid skill name %q: length must be 1-64 characters", name)
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("invalid skill name %q: must not start or end with a hyphen", name)
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("invalid skill name %q: must not contain consecutive hyphens", name)
	}
	return nil
}

// Validate parses dir/SKILL.md and checks the name rules and that the name matches the directory name.
func Validate(dir string) (Meta, error) {
	meta, err := ParseFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Meta{}, fmt.Errorf("validate skill: %w", err)
	}
	if err := ValidName(meta.Name); err != nil {
		return Meta{}, fmt.Errorf("validate skill: %w", err)
	}
	base := filepath.Base(dir)
	if meta.Name != base {
		return Meta{}, fmt.Errorf("skill name %q does not match directory %q", meta.Name, base)
	}
	if meta.Description == "" {
		return Meta{}, fmt.Errorf("skill description is empty")
	}
	return meta, nil
}

// Hash returns the content hash of a skill directory.
func Hash(dir string) (string, error) {
	sum, err := dirhash.HashDir(dir, "", dirhash.Hash1)
	if err != nil {
		return "", fmt.Errorf("hash skill directory: %w", err)
	}
	return sum, nil
}

// frontmatter returns the YAML between the opening and closing "---" lines.
// The first line of data must be exactly "---". Line endings may be LF or CRLF.
func frontmatter(data []byte) ([]byte, error) {
	line, rest, ok := cutLine(data)
	if !ok || line != "---" {
		return nil, errors.New("no frontmatter")
	}
	var b bytes.Buffer
	for {
		line, rest, ok = cutLine(rest)
		if !ok {
			return nil, errors.New("no frontmatter")
		}
		if line == "---" {
			return b.Bytes(), nil
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func cutLine(data []byte) (string, []byte, bool) {
	if len(data) == 0 {
		return "", nil, false
	}
	i := bytes.IndexByte(data, '\n')
	var line, rest []byte
	if i < 0 {
		line = data
	} else {
		line = data[:i]
		rest = data[i+1:]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return string(line), rest, true
}

func isNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}
