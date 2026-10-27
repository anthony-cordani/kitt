package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func remoteScope(t *testing.T, global bool) (string, []string) {
	t.Helper()
	testutil.IsolateHome(t)
	project := t.TempDir()
	t.Chdir(project)
	if global {
		path, err := manifest.GlobalPath()
		if err != nil {
			t.Fatal(err)
		}
		return path, []string{"-g"}
	}
	return filepath.Join(project, manifest.FileName), nil
}

func runRemote(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"remote"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func remoteManifestBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunRemoteListSorted(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "global"}[global], func(t *testing.T) {
			path, args := remoteScope(t, global)
			// Listing must not need reachable sources.
			testutil.WriteFile(t, path, "[sources]\nzulu = \"z-url\"\nalpha = \"a-url\"\n")
			before := remoteManifestBytes(t, path)
			code, stdout, stderr := runRemote(args...)
			if code != 0 || stdout != "alpha\ta-url\nzulu\tz-url\n" || stderr != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			if !bytes.Equal(before, remoteManifestBytes(t, path)) {
				t.Fatal("listing changed manifest")
			}
			testutil.WriteFile(t, path, "[sources]\n")
			code, stdout, stderr = runRemote(args...)
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("empty list: exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
		})
	}
}

func TestRunRemoteAdd(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "global"}[global], func(t *testing.T) {
			path, args := remoteScope(t, global)
			if !global {
				testutil.WriteFile(t, path, "[sources]\n")
			}
			url := testutil.URL(testutil.NewRepo(t))
			code, stdout, stderr := runRemote(append(args, "add", "team", url)...)
			want := "added source team\n"
			if global {
				want = "created " + path + "\n" + want
			}
			if code != 0 || stdout != want || stderr != "" {
				t.Fatalf("add: exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			m, err := manifest.Load(path)
			if err != nil || m.Sources["team"] != url {
				t.Fatalf("manifest = %+v, err = %v", m, err)
			}
			before := remoteManifestBytes(t, path)
			cases := []struct {
				name, url, err string
			}{
				{"team", url, ""},
				{"team", testutil.URL(testutil.NewRepo(t)), "kitt remote: source team already set to " + url + "\n"},
				{"Bad_Name", url, "invalid source name Bad_Name:"},
				{"other", filepath.Join(t.TempDir(), "missing"), "not a reachable git repository"},
			}
			for _, tc := range cases {
				code, stdout, stderr := runRemote(append(args, "add", tc.name, tc.url)...)
				if tc.err == "" {
					if code != 0 || stdout != "" || stderr != "" {
						t.Fatalf("same URL: exit %d, stdout %q, stderr %q", code, stdout, stderr)
					}
				} else if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "kitt remote: ") || !strings.Contains(stderr, tc.err) {
					t.Fatalf("add %s: exit %d, stdout %q, stderr %q", tc.name, code, stdout, stderr)
				}
				if !bytes.Equal(before, remoteManifestBytes(t, path)) {
					t.Fatalf("add %s changed manifest", tc.name)
				}
			}
		})
	}
}

func TestRunRemoteRemove(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "global"}[global], func(t *testing.T) {
			path, args := remoteScope(t, global)
			m := &manifest.Manifest{
				Sources: map[string]string{"used": "used-url", "spare": "spare-url"},
				Skills: map[string]*manifest.Skill{
					"zulu":  {Source: "used", Resolved: &manifest.Resolved{Commit: "z", Hash: "h1:z"}},
					"alpha": {Source: "used", Resolved: &manifest.Resolved{Commit: "a", Hash: "h1:a"}},
				},
			}
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			before := remoteManifestBytes(t, path)
			for _, tc := range []struct{ alias, err string }{
				{"used", "source used is used by alpha, zulu: remove or move those skills first"},
				{"missing", "unknown source missing"},
			} {
				code, stdout, stderr := runRemote(append(args, "rm", tc.alias)...)
				if code != 1 || stdout != "" || stderr != "kitt remote: "+tc.err+"\n" {
					t.Fatalf("rm %s: exit %d, stdout %q, stderr %q", tc.alias, code, stdout, stderr)
				}
				if !bytes.Equal(before, remoteManifestBytes(t, path)) {
					t.Fatalf("rm %s changed manifest", tc.alias)
				}
			}
			code, stdout, stderr := runRemote(append(args, "rm", "spare")...)
			if code != 0 || stdout != "removed source spare\n" || stderr != "" {
				t.Fatalf("rm: exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			saved, err := manifest.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := saved.Sources["spare"]; ok || len(saved.Sources) != 1 || saved.Sources["used"] != "used-url" || len(saved.Skills) != 2 || saved.Skills["alpha"].Resolved.Commit != "a" {
				t.Fatalf("saved manifest = %+v", saved)
			}
		})
	}
}

func TestRunRemoteMissingManifest(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "global"}[global], func(t *testing.T) {
			path, args := remoteScope(t, global)
			code, stdout, stderr := runRemote(args...)
			if global {
				if code != 0 || stdout != "" || stderr != "" {
					t.Fatalf("list: exit %d, stdout %q, stderr %q", code, stdout, stderr)
				}
			} else if code != 1 || stdout != "" || stderr != "kitt remote: no kitt.toml here\n" {
				t.Fatalf("list: exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			code, stdout, stderr = runRemote(append(args, "rm", "team")...)
			want := "kitt remote: no kitt.toml here\n"
			if global {
				want = "kitt remote: no user kitt.toml\n"
			}
			if code != 1 || stdout != "" || stderr != want {
				t.Fatalf("rm: exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			if !global {
				code, stdout, stderr = runRemote("add", "team", testutil.URL(testutil.NewRepo(t)))
				if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "kitt remote: no kitt.toml here:") {
					t.Fatalf("add: exit %d, stdout %q, stderr %q", code, stdout, stderr)
				}
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("manifest stat = %v, want not exist", err)
			}
		})
	}
}

func TestRunRemoteUsage(t *testing.T) {
	remoteScope(t, false)
	for _, args := range [][]string{
		{"add"}, {"add", "team"}, {"add", "team", "url", "extra"},
		{"rm"}, {"rm", "team", "extra"}, {"add", "-g", "team", "url"},
		{"rm", "team", "-g"}, {"unknown"}, {""},
	} {
		code, stdout, stderr := runRemote(args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "Usage: kitt remote [-g] [add <name> <url> | rm <name>]") {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
		if args[0] == "unknown" && !strings.HasPrefix(stderr, "kitt remote: unknown subcommand unknown\n") {
			t.Fatalf("unknown subcommand stderr = %q", stderr)
		}
	}
}
