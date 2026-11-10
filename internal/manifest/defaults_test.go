package manifest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestDefaultsSkipRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), manifest.FileName)
	testutil.WriteFile(t, path, "[sources]\nnexus = \"file:///skills\"\n[defaults]\nskip = [\"nexus/adr\", \"nexus/code-review\"]\n")
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := &manifest.Defaults{Skip: []string{"nexus/adr", "nexus/code-review"}}
	if !reflect.DeepEqual(m.Defaults, want) {
		t.Fatalf("defaults = %+v, want %+v", m.Defaults, want)
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Defaults, want) {
		t.Fatalf("defaults after Save = %+v, want %+v", got.Defaults, want)
	}
}

func TestAbsentDefaultsPreservesManifestBytes(t *testing.T) {
	// This is the encoder's existing format before the defaults table was added.
	const original = "[sources]\n  demo = \"file:///skills\"\n\n[skills]\n  [skills.review]\n    source = \"demo\"\n    version = \"^1.0.0\"\n    [skills.review.resolved]\n      version = \"1.0.0\"\n      commit = \"0123456789abcdef0123456789abcdef01234567\"\n      hash = \"h1:example\"\n"
	path := filepath.Join(t.TempDir(), manifest.FileName)
	testutil.WriteFile(t, path, original)
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Defaults != nil {
		t.Fatalf("absent defaults = %+v", m.Defaults)
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("manifest changed:\n%s", got)
	}
}
