package manifest_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	const (
		commit = "0123456789abcdef0123456789abcdef01234567"
		hash   = "h1:0123456789abcdef"
	)
	in := &manifest.Manifest{
		Sources: map[string]string{
			"alpha": "file:///alpha.git",
			"beta":  "file:///beta.git",
		},
		Skills: map[string]*manifest.Skill{
			"review": {
				Source:  "alpha",
				Version: "^1.2",
				Resolved: &manifest.Resolved{
					Version: "1.4.0",
					Commit:  commit,
					Hash:    hash,
				},
			},
			"pdf-processing": {
				Source:  "beta",
				Version: "1.0.0",
			},
		},
	}
	path := filepath.Join(t.TempDir(), "kitt.toml")
	if err := in.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Sources == nil || got.Skills == nil {
		t.Fatal("round trip produced nil maps")
	}
	if len(got.Sources) != 2 || got.Sources["alpha"] != "file:///alpha.git" || got.Sources["beta"] != "file:///beta.git" {
		t.Fatalf("sources = %#v", got.Sources)
	}
	if len(got.Skills) != 2 {
		t.Fatalf("skills = %#v", got.Skills)
	}
	review := got.Skills["review"]
	if review == nil || review.Source != "alpha" || review.Version != "^1.2" || review.Resolved == nil ||
		review.Resolved.Version != "1.4.0" || review.Resolved.Commit != commit || review.Resolved.Hash != hash {
		t.Fatalf("review = %+v", review)
	}
	pdf := got.Skills["pdf-processing"]
	if pdf == nil || pdf.Source != "beta" || pdf.Version != "1.0.0" || pdf.Resolved != nil {
		t.Fatalf("pdf-processing = %+v", pdf)
	}
}

func TestLoadEmptyFileMapsNonNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitt.toml")
	testutil.WriteFile(t, path, "")
	got, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Sources == nil || got.Skills == nil {
		t.Fatalf("Load empty = %+v, want non-nil maps", got)
	}
	if len(got.Sources) != 0 || len(got.Skills) != 0 {
		t.Fatalf("Load empty = sources %#v skills %#v, want empty maps", got.Sources, got.Skills)
	}
}

func TestLoadMissingFileIsNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitt.toml")
	_, err := manifest.Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load missing = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadUnknownKeyNamesTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kitt.toml")
	testutil.WriteFile(t, path, "surprise = true\n")
	_, err := manifest.Load(path)
	if err == nil || !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("Load unknown = %v, want an error naming surprise", err)
	}
}

func TestGlobalPathEndsWithKittToml(t *testing.T) {
	home := testutil.IsolateHome(t)
	got, err := manifest.GlobalPath()
	if err != nil {
		t.Fatalf("GlobalPath: %v", err)
	}
	suffix := filepath.Join("kitt", "kitt.toml")
	if !strings.HasSuffix(got, suffix) {
		t.Fatalf("GlobalPath() = %q, want suffix %q", got, suffix)
	}
	rel, err := filepath.Rel(home, got)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("GlobalPath() = %q, want it under isolated home %s", got, home)
	}
}
