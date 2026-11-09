package scaffold

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/templates"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestInitExistingAgentsKittSection(t *testing.T) {
	const prefix = "# Project\n\nOriginal project notes.\n\n"
	const suffix = "\n## More\n\nKeep these notes too.\n"
	const memory = "## Project memory\n\n" + templates.DocsStart + "\n- _No project docs yet._\n<!-- kitt:docs:end -->\n"
	const legacy = "## Skills\n\n" + templates.LegacySkillsLine
	const custom = "## Skills\n\nUse KITT to install our skills.\n"
	const marked = templates.KittStart + "\n## Skills\n\nCustom setup.\n" + templates.KittEnd + "\n"
	cases := []struct {
		name, body, kept            string
		changed, mention, hasMemory bool
	}{
		{name: "marked", body: marked, kept: marked},
		{name: "legacy LF", body: legacy + "\n", changed: true},
		{name: "legacy CRLF", body: strings.ReplaceAll(legacy+"\n", "\n", "\r\n"), changed: true},
		{name: "legacy no final newline", body: legacy, changed: true},
		{name: "legacy CRLF no final newline", body: strings.ReplaceAll(legacy, "\n", "\r\n"), changed: true},
		{name: "mention", body: custom, kept: custom, mention: true},
		{name: "missing", changed: true},
		{name: "word boundary", body: "A kitten lives here.\n", kept: "A kitten lives here.\n", changed: true},
		{name: "setup pending only", body: "> **Setup pending.** kitt needs setup.\n", kept: "> **Setup pending.** kitt needs setup.\n", changed: true},
		{name: "indented marker and setup pending", body: "  <!-- kitt:bootstrap -->\n  > **Setup pending.** kitt needs setup.\n", kept: "  <!-- kitt:bootstrap -->\n  > **Setup pending.** kitt needs setup.\n", changed: true},
		{name: "memory present", body: memory, kept: "## Project memory", changed: true, hasMemory: true},
		{name: "marked memory present", body: memory + marked, kept: marked, hasMemory: true},
		{name: "mention memory present", body: memory + custom, kept: custom, mention: true, hasMemory: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			root := testutil.NewRepo(t)
			path := filepath.Join(root, "AGENTS.md")
			original := prefix + tc.body
			// Exercise a legacy section at EOF as well as one followed by user text.
			if !strings.Contains(tc.name, "no final newline") {
				original += suffix
			}
			testutil.WriteFile(t, path, original)
			var out bytes.Buffer
			opts := Options{Root: root, Sources: demoSources(t), Out: &out}
			if err := Init(opts); err != nil {
				t.Fatal(err)
			}
			first := readFile(t, path)
			if !strings.HasPrefix(first, prefix) || !strings.Contains(first, tc.kept) {
				t.Fatalf("original notes lost:\n%s", first)
			}
			if !strings.Contains(tc.name, "no final newline") && !strings.Contains(first, suffix) {
				t.Fatalf("following text lost:\n%s", first)
			}
			if strings.Count(first, "## Project memory") != 1 || strings.Count(first, "## Skills") != 1 {
				t.Fatalf("section counts:\n%s", first)
			}
			wantKitt := 1
			if tc.mention {
				wantKitt = 0
			}
			if strings.Count(first, templates.KittStart) != wantKitt || strings.Count(first, templates.KittEnd) != wantKitt {
				t.Fatalf("kitt section count:\n%s", first)
			}
			if tc.changed && (!strings.Contains(first, templates.KittSection) || strings.Contains(first, templates.LegacySkillsLine)) {
				t.Fatalf("kitt section not updated:\n%s", first)
			}
			var want []string
			if !tc.hasMemory {
				want = append(want, "updated AGENTS.md (project memory section)")
			}
			if tc.changed {
				want = append(want, "updated AGENTS.md (kitt section)")
			}
			if tc.mention {
				want = append(want, "kept AGENTS.md kitt text (check it tells assistants how to install kitt)")
			}
			if len(want) == 0 {
				want = append(want, "kept AGENTS.md")
			}
			var got []string
			for _, line := range outputLines(out.String()) {
				if strings.Contains(line, "AGENTS.md") && line != "updated AGENTS.md docs index" {
					got = append(got, line)
				}
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("AGENTS messages = %q, want %q", got, want)
			}
			if tc.hasMemory && !tc.changed && first != original {
				t.Fatal("existing file changed")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, root)
			for i := 0; i < 2; i++ {
				out.Reset()
				if err := Init(opts); err != nil {
					t.Fatal(err)
				}
				for _, line := range outputLines(out.String()) {
					if !strings.HasPrefix(line, "kept ") {
						t.Errorf("repeat init: %q", line)
					}
				}
				diffSnapshots(t, before, snapshotTree(t, root))
				after, err := os.Stat(path)
				if err != nil || !after.ModTime().Equal(info.ModTime()) {
					t.Fatalf("repeat init rewrote AGENTS.md: %v", err)
				}
			}
		})
	}
}

func TestInitKittSectionMarkerTakesPrecedence(t *testing.T) {
	root := t.TempDir()
	original := templates.DocsStart + "\n" + templates.DocsEnd + "\n" + templates.KittStart + "\n## Skills\n\n" + templates.LegacySkillsLine
	path := filepath.Join(root, "AGENTS.md")
	testutil.WriteFile(t, path, original)
	if _, err := initAgents(root, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != original {
		t.Fatalf("marked section changed: %q", got)
	}
}
