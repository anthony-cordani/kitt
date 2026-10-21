package install

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
	"github.com/anthony-cordani/kitt/internal/templates"
	"github.com/anthony-cordani/kitt/internal/testutil"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallDestinationPreflight(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project with spaces é")
	pathsAt := func(root string) paths {
		return paths{skillsDir: filepath.Join(root, ".agents", "skills"), claudeDir: filepath.Join(root, ".claude", "skills")}
	}
	if err := preflightDestination(pathsAt(root), "review"); err != nil {
		t.Fatalf("spaces and non-ASCII install root: %v", err)
	}
	if runtime.GOOS != "windows" {
		return
	}
	for _, tc := range []struct{ root, name, want string }{
		{root, "con", "reserved device name"},
		{root, "com1", "reserved device name"},
		{root + "&", "review", "cannot create a junction"},
		{filepath.Join(root, strings.Repeat("long", 65)), "review", "too long"},
	} {
		err := preflightDestination(pathsAt(tc.root), tc.name)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.name) || !strings.Contains(err.Error(), tc.root) {
			t.Fatalf("preflight %s/%s: %v", tc.root, tc.name, err)
		}
	}
}

func TestStagingInsideSkillsAndSweepsOnlyOlderPlans(t *testing.T) {
	dir := t.TempDir()
	oldPlan := filepath.Join(dir, tempPrefix+"plan-old")
	freshPlan := filepath.Join(dir, tempPrefix+"plan-fresh")
	oldBackup := filepath.Join(dir, tempPrefix+"backup-old")
	freshBackup := filepath.Join(dir, tempPrefix+"backup-fresh")
	for _, path := range []string{oldPlan, freshPlan, oldBackup, freshBackup} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	abandoned := started.Add(-(abandonedStagingAge + time.Hour))
	for _, path := range []string{oldPlan, oldBackup} {
		if err := os.Chtimes(path, abandoned, abandoned); err != nil {
			t.Fatal(err)
		}
	}
	if err := sweepStaging(dir, started); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oldPlan, oldBackup} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists: %v", path, err)
		}
	}
	for _, path := range []string{freshPlan, freshBackup} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("current staging must survive: %s: %v", path, err)
		}
	}
}

func TestSweepIgnoresUnreadableDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t)
	if err := sweepStaging(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr(), "warning:") {
		t.Fatalf("stderr = %q", stderr())
	}
}

func transactionSnapshot(t *testing.T, p paths) map[string]string {
	t.Helper()
	result := map[string]string{}
	roots := []string{p.skillsDir, p.claudeDir, p.manifest}
	if !p.global {
		for _, name := range []string{".gitignore", "AGENTS.md", "CLAUDE.md"} {
			roots = append(roots, filepath.Join(p.root, name))
		}
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := info.Mode().String()
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				value += target
			} else if !entry.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				value += string(data)
			}
			result[path] = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func transactionFixture(t *testing.T, global bool) (paths, Options, string) {
	t.Helper()
	home := testutil.IsolateHome(t)
	root, repo := testutil.NewRepo(t), testutil.NewRepo(t)
	opts := Options{Root: root, Global: global}
	p, err := locate(opts)
	if err != nil {
		t.Fatal(err)
	}
	if global {
		p.root = home
	}
	testutil.WriteFile(t, p.manifest, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	return p, opts, repo
}

func transactionRelease(t *testing.T, repo, name, version string, requires ...string) {
	t.Helper()
	testutil.WriteSkillRequires(t, repo, name, version, requires)
	testutil.Commit(t, repo, name+version)
	testutil.Git(t, repo, "tag", name+"/v"+version)
}

func TestApplyTransactionRollbackAndRetry(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, failure := range []string{"rename", "finish", "ownership", "manifest", "gitignore"} {
			if global && failure == "gitignore" {
				continue
			}
			t.Run(fmt.Sprintf("global=%t/%s", global, failure), func(t *testing.T) {
				p, opts, repo := transactionFixture(t, global)
				transactionRelease(t, repo, "leaf", "1.0.0")
				transactionRelease(t, repo, "root", "1.0.0", "leaf@^1")
				if !global {
					testutil.WriteFile(t, filepath.Join(p.root, "AGENTS.md"), templates.BootstrapMarker+"\n"+templates.DocsStart+"\nold index\n"+templates.DocsEnd+"\n")
				}
				if err := Add(opts, "root"); err != nil {
					t.Fatal(err)
				}
				transactionRelease(t, repo, "extra", "1.0.0")
				transactionRelease(t, repo, "leaf", "2.0.0")
				transactionRelease(t, repo, "root", "2.0.0", "leaf@^2", "extra@^1")
				if !global {
					if err := os.Remove(filepath.Join(p.root, "CLAUDE.md")); err != nil {
						t.Fatal(err)
					}
					testutil.WriteFile(t, filepath.Join(p.root, ".agents", "docs", "new.md"), "---\ntitle: New doc\n---\n")
				}
				gitignore := filepath.Join(p.root, ".gitignore")
				if failure == "gitignore" {
					testutil.WriteFile(t, gitignore, "user entry\n")
					if err := os.Chmod(gitignore, 0o444); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(gitignore, 0o644) })
				}
				before := transactionSnapshot(t, p)
				fired := false
				applyTestHook = func(step, path string) error {
					if step == "rename" {
						dirs, err := filepath.Glob(filepath.Join(p.skillsDir, tempPrefix+"plan-*"))
						if err != nil || len(dirs) != 1 {
							t.Fatalf("staging outside skills: %v, %v", dirs, err)
						}
						if filepath.Base(path) == "root" {
							if _, err := os.Stat(filepath.Join(p.skillsDir, "extra", "SKILL.md")); err != nil {
								t.Fatalf("failure did not follow managed writes: %v", err)
							}
						}
					}
					if step == failure && (step != "rename" || filepath.Base(path) == "root") {
						fired = true
						return fmt.Errorf("injected %s failure at %s", step, path)
					}
					return nil
				}
				t.Cleanup(func() { applyTestHook = nil })
				err := Add(opts, "root@2.0.0")
				applyTestHook = nil
				if failure == "gitignore" {
					if err == nil {
						t.Fatal("read-only .gitignore did not fail")
					}
				} else if !fired {
					t.Fatalf("%s hook did not fire: %v", failure, err)
				}
				var pe *dependency.PlanError
				if err == nil || errors.As(err, &pe) {
					t.Fatalf("rollback diagnostic: %v", err)
				}
				if after := transactionSnapshot(t, p); !reflect.DeepEqual(before, after) {
					t.Fatalf("rollback changed managed state:\nbefore %v\nafter %v", before, after)
				}
				if failure == "gitignore" {
					if err := os.Chmod(gitignore, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := Add(opts, "root@2.0.0"); err != nil {
					t.Fatalf("retry failed: %v", err)
				}
				m, err := manifest.Load(p.manifest)
				if err != nil {
					t.Fatal(err)
				}
				if m.Skills["leaf"].Resolved.Version != "2.0.0" || m.Skills["extra"] == nil {
					t.Fatal("retry did not complete")
				}
			})
		}
	}
}

func TestRollbackFailureNamesRecoveryPaths(t *testing.T) {
	p, _, _ := transactionFixture(t, false)
	if err := os.MkdirAll(p.skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p.installed = &installedState{dir: p.skillsDir, names: map[string]bool{}, deferred: true}
	dest := filepath.Join(p.skillsDir, "leaf")
	testutil.WriteFile(t, filepath.Join(dest, "original"), "old bytes")
	tx, err := newApplyTransaction(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.moveAside(dest); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dest, "new"), "new bytes")
	applyTestHook = func(step, path string) error {
		if step == "rollback" && path == dest {
			return errors.New("locked")
		}
		return nil
	}
	t.Cleanup(func() { applyTestHook = nil })
	err = tx.rollback()
	if err == nil || !strings.Contains(err.Error(), dest) || !strings.Contains(err.Error(), tx.moved[0].backup) || !strings.Contains(err.Error(), "manual attention") || strings.Contains(err.Error(), "no changes applied") {
		t.Fatalf("recovery diagnostic: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tx.moved[0].backup, "original"))
	if err != nil || string(data) != "old bytes" {
		t.Fatalf("backup lost: %s, %v", data, err)
	}
}

func TestExactSourceErrorsKeepOperationContext(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "foo", "1.0.0")
	for _, operation := range []string{"open", "fetch"} {
		t.Run(operation, func(t *testing.T) {
			access := &installSources{current: &manifest.Manifest{Sources: map[string]string{"demo": testutil.URL(repo)}}, opened: map[string]*source.Source{}}
			if operation == "fetch" {
				if _, err := access.exact("demo", ""); err != nil {
					t.Fatal(err)
				}
			}
			url := testutil.URL(filepath.Join(t.TempDir(), "missing"))
			if operation == "open" {
				access.current.Sources["demo"] = url
			} else {
				if err := os.RemoveAll(repo); err != nil {
					t.Fatal(err)
				}
			}
			_, err := access.exact("demo", "not-a-commit")
			if err == nil || !strings.Contains(err.Error(), operation+" source demo") {
				t.Fatalf("context: %v", err)
			}
		})
	}
}

func TestFirstApplyRollbackAndRetry(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			p, opts, repo := transactionFixture(t, global)
			transactionRelease(t, repo, "leaf", "1.0.0")
			transactionRelease(t, repo, "root", "1.0.0", "leaf@^1")
			before := transactionSnapshot(t, p)
			fired := false
			applyTestHook = func(step, path string) error {
				if step == "rename" {
					data, err := os.ReadFile(filepath.Join(p.skillsDir, installedFile))
					if err != nil || !strings.Contains(string(data), filepath.Base(path)+"\n") {
						t.Fatalf("directory published before ownership: %s, %v", data, err)
					}
					if filepath.Base(path) == "root" {
						fired = true
						return errors.New("injected root rename failure")
					}
				}
				return nil
			}
			t.Cleanup(func() { applyTestHook = nil })
			err := Add(opts, "root")
			applyTestHook = nil
			var pe *dependency.PlanError
			if !fired || err == nil || errors.As(err, &pe) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(before, transactionSnapshot(t, p)) {
				t.Fatal("first install rollback changed managed state")
			}
			for _, dir := range []string{".agents", ".claude"} {
				if _, err := os.Lstat(filepath.Join(p.root, dir)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("left %s after rollback: %v", dir, err)
				}
			}
			if err := Add(opts, "root"); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}

func TestFinishRollbackRestoresSymlinkedFileBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("project file symlink requires Windows privilege")
	}
	p, opts, repo := transactionFixture(t, false)
	transactionRelease(t, repo, "foo", "1.0.0")
	target := filepath.Join(p.root, "ignore-target")
	testutil.WriteFile(t, target, "original ignore bytes\n")
	if err := os.Symlink("ignore-target", filepath.Join(p.root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	applyTestHook = func(step, path string) error {
		if step == "manifest" {
			return errors.New("manifest failure")
		}
		return nil
	}
	t.Cleanup(func() { applyTestHook = nil })
	err := Add(opts, "foo")
	applyTestHook = nil
	var pe *dependency.PlanError
	if err == nil || errors.As(err, &pe) {
		t.Fatalf("rollback: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original ignore bytes\n" {
		t.Fatalf("referent changed: %s, %v", data, err)
	}
	link, err := os.Readlink(filepath.Join(p.root, ".gitignore"))
	if err != nil || link != "ignore-target" {
		t.Fatalf("link changed: %s, %v", link, err)
	}
	if err := Add(opts, "foo"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestJunctionBackupRollbackAndCleanupPreserveTarget(t *testing.T) {
	p, _, _ := transactionFixture(t, false)
	if err := os.MkdirAll(p.skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p.installed = &installedState{dir: p.skillsDir, names: map[string]bool{}, deferred: true}
	target := filepath.Join(p.skillsDir, "leaf")
	testutil.WriteFile(t, filepath.Join(target, "content"), "must survive")
	if err := createLink(p, filepath.Join(p.claudeDir, "leaf"), target, "leaf"); err != nil {
		t.Fatal(err)
	}
	tx, err := newApplyTransaction(p)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.claudeDir, "leaf")
	if err := tx.moveAside(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link not moved: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "content")); err != nil || string(data) != "must survive" {
		t.Fatalf("move changed target: %s, %v", data, err)
	}
	if err := createLink(p, link, target, "leaf"); err != nil {
		t.Fatal(err)
	}
	if err := tx.rollback(); err != nil {
		t.Fatal(err)
	}
	if !sameDir(link, target) {
		t.Fatal("link not restored")
	}
	// Removing a backup must also leave the linked directory's content intact.
	tx, err = newApplyTransaction(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.moveAside(link); err != nil {
		t.Fatal(err)
	}
	if err := removeTree(tx.root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "content")); err != nil || string(data) != "must survive" {
		t.Fatalf("cleanup changed target: %s, %v", data, err)
	}
}

func TestBackupRemovalFailureStillReportsSuccess(t *testing.T) {
	p, opts, repo := transactionFixture(t, false)
	transactionRelease(t, repo, "foo", "1.0.0")
	var locked string
	applyTestHook = func(step, path string) error {
		if step != "manifest" {
			return nil
		}
		matches, err := filepath.Glob(filepath.Join(p.skillsDir, tempPrefix+"backup-*"))
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return fmt.Errorf("backup dirs = %v", matches)
		}
		locked = filepath.Join(matches[0], "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(locked, "keep"), []byte("x"), 0o644); err != nil {
			return err
		}
		if err := os.Chmod(locked, 0); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() {
		applyTestHook = nil
		if locked != "" {
			_ = os.Chmod(locked, 0o755)
		}
	})
	stderr := captureStderr(t)
	var stdout bytes.Buffer
	opts.Out = &stdout
	if err := Add(opts, "foo"); err != nil {
		t.Fatalf("install: %v\nstderr: %s", err, stderr())
	}
	if !strings.Contains(stdout.String(), "added foo 1.0.0") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if got := stderr(); !strings.Contains(got, "warning:") || !strings.Contains(got, filepath.Join(p.skillsDir, tempPrefix+"backup-")) {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(filepath.Join(p.skillsDir, "foo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	stopped := false
	stop := func() string {
		t.Helper()
		if !stopped {
			stopped = true
			os.Stderr = old
			_ = w.Close()
			<-done
			_ = r.Close()
		}
		return buf.String()
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}
