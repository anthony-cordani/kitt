package manifest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestDependencyFieldsRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		direct   string
		requires string
		want     []string
	}{
		{name: "legacy"},
		{name: "known_leaf", direct: "direct = false\n", requires: "requires = []\n", want: []string{}},
		{name: "requirements", direct: "direct = true\n", requires: "requires = [\"team/code-review@^1\", \"other/us-review@1.2.3\"]\n", want: []string{"team/code-review@^1", "other/us-review@1.2.3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kitt.toml")
			text := "[sources]\nteam = \"file:///team.git\"\n[skills.review]\nsource = \"team\"\n" + tt.direct + "via = [\"parent\"]\n[skills.review.resolved]\nversion = \"1.2.3\"\ncommit = \"abc\"\nhash = \"h1:abc\"\n" + tt.requires
			testutil.WriteFile(t, path, text)
			for i := 0; i < 2; i++ {
				m, err := manifest.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				s := m.Skills["review"]
				if s.IsDirect() != (tt.direct != "direct = false\n") || (s.Direct == nil) != (tt.direct == "") {
					t.Fatalf("direct = %v, IsDirect = %v", s.Direct, s.IsDirect())
				}
				if !reflect.DeepEqual(s.Via, []string{"parent"}) || s.Resolved == nil {
					t.Fatalf("skill = %+v", s)
				}
				if (s.Resolved.Requires == nil) != (tt.requires == "") {
					t.Fatalf("requires = %#v, want presence %v", s.Resolved.Requires, tt.requires != "")
				}
				if s.Resolved.Requires != nil && !reflect.DeepEqual(*s.Resolved.Requires, tt.want) {
					t.Fatalf("requires = %#v, want %#v", *s.Resolved.Requires, tt.want)
				}
				if err := m.Save(path); err != nil {
					t.Fatal(err)
				}
				encoded := readManifest(t, path)
				if strings.Contains(encoded, "direct =") != (tt.direct != "") || strings.Contains(encoded, "requires =") != (tt.requires != "") {
					t.Fatalf("field presence changed:\n%s", encoded)
				}
			}
		})
	}
}

func TestSetVia(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil"},
		{name: "empty", in: []string{}, want: []string{}},
		{name: "sorted_unique", in: []string{"zeta", "alpha", "zeta", "beta", "alpha"}, want: []string{"alpha", "beta", "zeta"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string{}, tt.in...)
			s := &manifest.Skill{Source: "team", Via: []string{"old"}}
			s.SetVia(tt.in)
			if !reflect.DeepEqual(s.Via, tt.want) || strings.Join(tt.in, ",") != strings.Join(before, ",") {
				t.Fatalf("SetVia = %#v, want %#v; input = %#v, was %#v", s.Via, tt.want, tt.in, before)
			}
			path := filepath.Join(t.TempDir(), "kitt.toml")
			m := &manifest.Manifest{Skills: map[string]*manifest.Skill{"review": s}}
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readManifest(t, path), "via =") != (len(tt.want) > 0) {
				t.Fatal("via field presence does not match nonempty parents")
			}
		})
	}
}

func TestLegacyManifestByteIdenticalRoundTrip(t *testing.T) {
	const legacy = `[sources]
  alpha = "file:///alpha.git"
  beta = "file:///beta.git"

[skills]
  [skills.pdf-processing]
    source = "beta"
    version = "1.0.0"
  [skills.review]
    source = "alpha"
    version = "^1.2"
    [skills.review.resolved]
      version = "1.4.0"
      commit = "0123456789abcdef0123456789abcdef01234567"
      hash = "h1:0123456789abcdef"
`
	path := filepath.Join(t.TempDir(), "kitt.toml")
	testutil.WriteFile(t, path, legacy)
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range m.Skills {
		if !s.IsDirect() || s.Direct != nil || s.Via != nil || (s.Resolved != nil && s.Resolved.Requires != nil) {
			t.Fatalf("legacy skill %q = %+v", name, s)
		}
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readManifest(t, path); got != legacy {
		t.Fatalf("legacy manifest changed:\ngot:\n%s\nwant:\n%s", got, legacy)
	}
}

func TestDependencyFieldsKeepUnknownKeyRejection(t *testing.T) {
	tests := []struct {
		name string
		text string
		key  string
	}{
		{name: "skill", text: "[skills.review]\nsource = \"team\"\ndirect = false\nvia = []\nsurprise = true\n", key: "skills.review.surprise"},
		{name: "resolved", text: "[skills.review.resolved]\nrequires = []\nsurprise = true\n", key: "skills.review.resolved.surprise"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kitt.toml")
			testutil.WriteFile(t, path, tt.text)
			_, err := manifest.Load(path)
			if err == nil || !strings.Contains(err.Error(), "unknown keys: "+tt.key) {
				t.Fatalf("Load = %v, want unknown key %q", err, tt.key)
			}
		})
	}
}

func readManifest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
