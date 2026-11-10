package install_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func writeDefaults(t *testing.T, repo, content string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), content)
	testutil.Commit(t, repo, "update defaults")
}

func mustAddDefaults(t *testing.T, project string) string {
	t.Helper()
	opts, out := withOpts(project, false)
	if err := install.AddDefaults(opts); err != nil {
		t.Fatalf("AddDefaults: %v", err)
	}
	return out.String()
}

func TestAddDefaultsMatchesAdd(t *testing.T) {
	for _, version := range []string{"1.2.3", ""} {
		t.Run("version="+version, func(t *testing.T) {
			project, repo := newPair(t)
			if version == "" {
				testutil.WriteSkill(t, repo, "adr", "", "unreleased")
				testutil.Commit(t, repo, "adr")
			} else {
				release(t, repo, "adr", version)
			}
			writeDefaults(t, repo, "skills = [\"adr\", \"adr\"]\n")
			sources := map[string]string{"demo": testutil.URL(repo)}
			writeKitt(t, project, sources)
			manual := testutil.NewRepo(t)
			writeKitt(t, manual, sources)
			manualOut := mustAdd(t, manual, "demo/adr")
			out := mustAddDefaults(t, project)
			if out != manualOut+"added default demo/adr\n" {
				t.Fatalf("output = %q, want %q", out, manualOut+"added default demo/adr\n")
			}
			if !reflect.DeepEqual(skillEntry(t, project, "adr"), skillEntry(t, manual, "adr")) {
				t.Fatal("default pin differs from Add pin")
			}
			assertLinkResolves(t, claudePath(project, "adr"), skillPath(project, "adr"))
		})
	}
}

func TestAddDefaultsKeepsExistingPin(t *testing.T) {
	for _, alias := range []string{"demo", "other"} {
		t.Run(alias, func(t *testing.T) {
			project, repo := standard(t)
			writeKitt(t, project, map[string]string{"demo": testutil.URL(repo), "other": testutil.URL(repo)})
			mustAdd(t, project, alias+"/review@1.0.0")
			writeDefaults(t, repo, "skills = [\"review\"]\n")
			before := snapshot(t, project)
			if out := mustAddDefaults(t, project); out != "" {
				t.Fatalf("pinned default output = %q", out)
			}
			assertResolved(t, skillEntry(t, project, "review"), "1.0.0", "1.0.0")
			assertSameTree(t, before, snapshot(t, project))
		})
	}
}

func TestAddDefaultsSkipSurvivesAdds(t *testing.T) {
	project, repo := standard(t)
	release(t, repo, "adr", "1.0.0")
	writeDefaults(t, repo, "skills = [\"review\", \"adr\"]\n")
	path := filepath.Join(project, manifest.FileName)
	m := loadManifest(t, path)
	m.Defaults = &manifest.Defaults{Skip: []string{"demo/adr"}}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	out := mustAddDefaults(t, project)
	want := "added review 2.0.0\nadded default demo/review\nskipped default demo/adr (kitt.toml [defaults] skip)\n"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if got := loadManifest(t, path); !reflect.DeepEqual(got.Defaults, m.Defaults) || len(got.Skills) != 1 {
		t.Fatalf("manifest = %+v", got)
	}
	assertMissing(t, skillPath(project, "adr"))
}

func TestAddDefaultsWarnsAndContinues(t *testing.T) {
	for _, tc := range []struct {
		name, defaults, warning string
		brokenInstall           bool
	}{
		{name: "unknown skill", defaults: "skills = [\"missing\", \"review\"]", warning: "default demo/missing:"},
		{name: "invalid name", defaults: "skills = [\"../bad\", \"review\"]", warning: "default demo/../bad:"},
		{name: "install error", defaults: "skills = [\"broken\", \"review\"]", warning: "default demo/broken:", brokenInstall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, repo := standard(t)
			if tc.brokenInstall {
				testutil.WriteSkillRequires(t, repo, "broken", "1.0.0", []string{"missing@^1"})
				testutil.Commit(t, repo, "broken dependency")
				testutil.Git(t, repo, "tag", "broken/v1.0.0")
			}
			writeDefaults(t, repo, tc.defaults+"\n")
			out := mustAddDefaults(t, project)
			if !lineWith(out, "warning: "+tc.warning) || strings.Count(out, "warning: ") != 1 || !strings.HasSuffix(out, "added review 2.0.0\nadded default demo/review\n") {
				t.Fatalf("output = %q", out)
			}
			assertResolved(t, skillEntry(t, project, "review"), "^2.0.0", "2.0.0")
			if len(loadManifest(t, filepath.Join(project, manifest.FileName)).Skills) != 1 {
				t.Fatal("failed default was pinned")
			}
		})
	}
}

func TestAddDefaultsSourceWarnings(t *testing.T) {
	for _, failure := range []string{"invalid file", "fetch"} {
		t.Run(failure, func(t *testing.T) {
			project, repo := standard(t)
			bad := testutil.NewRepo(t)
			writeDefaults(t, bad, "skills = [")
			if failure == "fetch" {
				bad = filepath.Join(bad, "missing")
			}
			writeKitt(t, project, map[string]string{"alpha": testutil.URL(bad), "demo": testutil.URL(repo)})
			writeDefaults(t, repo, "skills = [\"review\"]\n")
			// This is init's sequence; source-side failures must not fail it.
			mustRestore(t, project)
			out := mustAddDefaults(t, project)
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) != 3 || !strings.HasPrefix(lines[0], "warning: ") || !strings.Contains(lines[0], "alpha") || lines[2] != "added default demo/review" {
				t.Fatalf("output = %q", out)
			}
			assertResolved(t, skillEntry(t, project, "review"), "^2.0.0", "2.0.0")
		})
	}
}

func TestAddDefaultsSourceOrderAndFileOrder(t *testing.T) {
	project, alpha := newPair(t)
	zulu := testutil.NewRepo(t)
	for _, repo := range []string{alpha, zulu} {
		release(t, repo, "shared", "1.0.0")
	}
	release(t, alpha, "zebra", "1.0.0")
	release(t, zulu, "aardvark", "1.0.0")
	writeDefaults(t, alpha, "skills = [\"zebra\", \"shared\"]\n")
	writeDefaults(t, zulu, "skills = [\"shared\", \"aardvark\"]\n")
	writeKitt(t, project, map[string]string{"zulu": testutil.URL(zulu), "alpha": testutil.URL(alpha)})
	out := mustAddDefaults(t, project)
	want := "added zebra 1.0.0\nadded default alpha/zebra\nadded shared 1.0.0\nadded default alpha/shared\nadded aardvark 1.0.0\nadded default zulu/aardvark\n"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if sk := skillEntry(t, project, "shared"); sk.Source != "alpha" {
		t.Fatalf("shared source = %q, want alpha", sk.Source)
	}
}

func TestAddDefaultsInstallsClosureAndReloads(t *testing.T) {
	project, repo := newPair(t)
	release(t, repo, "leaf", "1.0.0")
	testutil.WriteSkillRequires(t, repo, "parent", "1.0.0", []string{"leaf@^1"})
	testutil.Commit(t, repo, "parent")
	testutil.Git(t, repo, "tag", "parent/v1.0.0")
	writeDefaults(t, repo, "skills = [\"parent\", \"leaf\"]\n")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(repo)})
	out := mustAddDefaults(t, project)
	if !strings.Contains(out, "added default demo/parent\n") || strings.Contains(out, "added default demo/leaf") {
		t.Fatalf("output = %q", out)
	}
	if leaf := skillEntry(t, project, "leaf"); leaf.IsDirect() || leaf.Resolved == nil || leaf.Resolved.Version != "1.0.0" {
		t.Fatalf("leaf = %+v", leaf)
	}
	assertLinkResolves(t, claudePath(project, "leaf"), skillPath(project, "leaf"))
}

func TestAddDefaultsAbsentIsSilent(t *testing.T) {
	project, _ := standard(t)
	before := snapshot(t, project)
	if out := mustAddDefaults(t, project); out != "" {
		t.Fatalf("output = %q", out)
	}
	assertSameTree(t, before, snapshot(t, project))
}

func TestAddDefaultsBrokenManifestErrors(t *testing.T) {
	for _, content := range []string{"missing", "[sources"} {
		t.Run(content, func(t *testing.T) {
			project := testutil.NewRepo(t)
			if content != "missing" {
				testutil.WriteFile(t, filepath.Join(project, manifest.FileName), content)
			}
			if err := install.AddDefaults(install.Options{Root: project}); err == nil {
				t.Fatal("AddDefaults accepted broken local manifest")
			}
		})
	}
}
