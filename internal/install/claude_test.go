package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/templates"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestRestoreDoesNotCreateClaudeMD(t *testing.T) {
	project, _ := standard(t)
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n")

	out := mustRestore(t, project)

	assertMissing(t, filepath.Join(project, "CLAUDE.md"))
	if got := readText(t, filepath.Join(project, ".gitignore")); got != kittBlock(false) {
		t.Fatalf("gitignore = %q, want %q", got, kittBlock(false))
	}
	if strings.Contains(out, "CLAUDE.md") {
		t.Fatalf("unexpected CLAUDE.md output: %q", out)
	}
}

func TestRestoreCleansUpOnlyManagedClaudeMD(t *testing.T) {
	for _, tc := range []struct {
		name, content, ignore string
		withoutAgents, remove bool
	}{
		{name: "owned", content: templates.ClaudeMD, ignore: kittBlock(true), remove: true},
		{name: "owned without agents", content: templates.ClaudeMD, ignore: kittBlock(true), withoutAgents: true, remove: true},
		{name: "custom content", content: "# Custom instructions\n", ignore: kittBlock(true)},
		{name: "extra newline", content: templates.ClaudeMD + "\n", ignore: kittBlock(true)},
		{name: "different line ending", content: "@AGENTS.md\r\n", ignore: kittBlock(true)},
		{name: "not listed", content: templates.ClaudeMD, ignore: kittBlock(false)},
		{name: "no gitignore", content: templates.ClaudeMD},
		{name: "listed outside block", content: templates.ClaudeMD, ignore: "/CLAUDE.md\n" + kittBlock(false)},
		{name: "no managed block", content: templates.ClaudeMD, ignore: "/CLAUDE.md\n"},
		{name: "similar line", content: templates.ClaudeMD, ignore: strings.Replace(kittBlock(true), "/CLAUDE.md\n", "/CLAUDE.md.bak\n", 1)},
		{name: "CRLF block", content: templates.ClaudeMD, ignore: strings.ReplaceAll(kittBlock(true), "\n", "\r\n"), remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _ := standard(t)
			path := filepath.Join(project, "CLAUDE.md")
			testutil.WriteFile(t, path, tc.content)
			if !tc.withoutAgents {
				testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n")
			}
			ignorePath := filepath.Join(project, ".gitignore")
			if tc.ignore != "" {
				testutil.WriteFile(t, ignorePath, tc.ignore)
			}

			out := mustRestore(t, project)

			if tc.remove {
				assertMissing(t, path)
				if out != "removed CLAUDE.md (Claude Code reads AGENTS.md)\n" {
					t.Fatalf("output = %q", out)
				}
			} else {
				if got := readText(t, path); got != tc.content {
					t.Fatalf("CLAUDE.md = %q, want %q", got, tc.content)
				}
				if strings.Contains(out, "CLAUDE.md") {
					t.Fatalf("unexpected CLAUDE.md output: %q", out)
				}
			}
			want := kittBlock(false)
			if tc.ignore != "" {
				// Only complete managed blocks are replaced; user entries stay.
				if i := strings.Index(tc.ignore, "# <<< kitt"); i >= 0 {
					want = tc.ignore[:strings.Index(tc.ignore, "# >>> kitt")] + want
				} else {
					want = tc.ignore + want
				}
			}
			if got := readText(t, ignorePath); got != want {
				t.Fatalf("gitignore = %q, want %q", got, want)
			}
			if out := mustRestore(t, project); strings.Contains(out, "CLAUDE.md") {
				t.Fatalf("second install mentioned CLAUDE.md: %q", out)
			}
		})
	}
}

func TestRestoreKeepsSymlinkedManagedClaudeMD(t *testing.T) {
	project, _ := standard(t)
	path := filepath.Join(project, "CLAUDE.md")
	target := filepath.Join(project, "claude-target")
	testutil.WriteFile(t, target, "@AGENTS.md\n")
	if err := os.Symlink(filepath.Base(target), path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	testutil.WriteFile(t, filepath.Join(project, ".gitignore"), kittBlock(true))

	mustRestore(t, project)

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat CLAUDE.md: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("CLAUDE.md symlink was removed")
	}
	if got, err := os.Readlink(path); err != nil || got != filepath.Base(target) {
		t.Fatalf("CLAUDE.md symlink = %q, %v", got, err)
	}
}

func TestRestoreKeepsClaudeMDDirectory(t *testing.T) {
	project, _ := standard(t)
	path := filepath.Join(project, "CLAUDE.md")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(path, "keep"), "user data\n")
	testutil.WriteFile(t, filepath.Join(project, ".gitignore"), kittBlock(true))

	mustRestore(t, project)

	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("CLAUDE.md directory was not kept: info=%v, err=%v", info, err)
	}
	if got := readText(t, filepath.Join(path, "keep")); got != "user data\n" {
		t.Fatalf("CLAUDE.md directory contents = %q", got)
	}
}
