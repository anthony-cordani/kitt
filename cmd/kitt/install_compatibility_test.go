package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestDependencyCLIErrorsHaveOneCommandPrefix(t *testing.T) {
	for _, command := range []string{"install", "upgrade", "init"} {
		t.Run(command, func(t *testing.T) {
			testutil.IsolateHome(t)
			project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
			path := filepath.Join(project, manifest.FileName)
			testutil.WriteFile(t, path, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
			if command == "upgrade" {
				testutil.Release(t, repo, "root", "1.0.0")
				if err := install.Add(install.Options{Root: project, Out: io.Discard}, "root"); err != nil {
					t.Fatal(err)
				}
			}
			testutil.WriteSkillRequires(t, repo, "root", "1.1.0", []string{"missing/leaf@^1"})
			testutil.Commit(t, repo, "bad dependency alias")
			testutil.Git(t, repo, "tag", "root/v1.1.0")
			args := []string{command, "root"}
			if command == "init" {
				m, err := manifest.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				m.Skills["root"] = &manifest.Skill{Source: "demo"}
				if err := m.Save(path); err != nil {
					t.Fatal(err)
				}
				args = []string{command}
			}
			t.Chdir(project)
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 1 {
				t.Fatalf("exit=%d stderr=%s", code, &stderr)
			}
			text := stderr.String()
			first, _, _ := strings.Cut(text, "\n")
			if !strings.HasPrefix(first, "kitt "+command+": ") || !strings.Contains(first, "unknown source alias") || strings.Count(first, "kitt ") != 1 {
				t.Fatalf("prefix: %s", text)
			}
		})
	}
}

func TestNonPlanningCLIErrorsMatchMain(t *testing.T) {
	for _, command := range []string{"install", "upgrade"} {
		for _, failure := range []string{"manifest", "unknown source", "fetch"} {
			if command == "upgrade" && failure == "unknown source" {
				continue
			}
			t.Run(command+"/"+failure, func(t *testing.T) {
				testutil.IsolateHome(t)
				project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
				path := filepath.Join(project, manifest.FileName)
				args := []string{command}
				want := ""
				if failure == "manifest" {
					want = fmt.Sprintf("kitt %s: kitt.toml not found in %s: run kitt init, or create it with a [sources] table\n", command, project)
				} else {
					testutil.Release(t, repo, "foo", "1.0.0")
					testutil.WriteFile(t, path, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
					if failure == "unknown source" {
						args = append(args, "missing/foo")
						want = "kitt install: unknown source missing\n"
					} else {
						if command == "upgrade" {
							if err := install.Add(install.Options{Root: project, Out: io.Discard}, "foo"); err != nil {
								t.Fatal(err)
							}
						}
						src, err := source.Open("demo", testutil.URL(repo))
						if err != nil {
							t.Fatal(err)
						}
						if err := os.RemoveAll(repo); err != nil {
							t.Fatal(err)
						}
						fetchErr := src.Fetch()
						if fetchErr == nil {
							t.Fatal("expected fetch failure")
						}
						args = append(args, "foo")
						want = fmt.Sprintf("kitt %s: fetch source demo: %s\n", command, fetchErr)
					}
				}
				t.Chdir(project)
				var out, stderr bytes.Buffer
				if code := run(args, &out, &stderr); code != 1 || stderr.String() != want {
					t.Fatalf("exit=%d stderr:\n%s\nwant:\n%s", code, &stderr, want)
				}
			})
		}
	}
}
