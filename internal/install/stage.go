package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
	"github.com/anthony-cordani/kitt/internal/templates"
)

type stagedSkill struct{ dir, hash string }

// Staging and backups stay inside skillsDir, on the destination volume.
func stagePlan(p paths, m *manifest.Manifest, plan *dependency.Plan, access *installSources, repair bool) (staged map[string]stagedSkill, cleanup func(), err error) {
	root := ""
	var created []string
	cleanup = func() {
		if root != "" {
			os.RemoveAll(root)
		}
		for _, dir := range created {
			_ = os.Remove(dir)
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	for dir := p.skillsDir; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		created = append(created, dir)
	}
	if err := os.MkdirAll(p.skillsDir, 0o755); err != nil {
		return nil, cleanup, err
	}
	staged = map[string]stagedSkill{}
	for _, n := range plan.Nodes {
		if err := skill.ValidName(n.Name); err != nil {
			return nil, cleanup, err
		}
		if strings.HasPrefix(n.Name, "kitt-") {
			return nil, cleanup, fmt.Errorf("skill %s: skill names starting with kitt- are reserved", n.Name)
		}
		if err := p.installed.check(n.Name); err != nil {
			return nil, cleanup, err
		}
		expected := ""
		if old := m.Skills[n.Name]; old != nil && old.Source == n.Source && old.Resolved != nil && old.Resolved.Commit == n.Commit {
			expected = old.Resolved.Hash
		}
		if !repair && expected != "" {
			staged[n.Name] = stagedSkill{hash: expected}
			continue
		}
		match, err := hashMatches(filepath.Join(p.skillsDir, n.Name), expected)
		if err != nil {
			return nil, cleanup, err
		}
		if match {
			staged[n.Name] = stagedSkill{hash: expected}
			continue
		}
		if root == "" {
			root, err = os.MkdirTemp(p.skillsDir, tempPrefix+"plan-")
			if err != nil {
				return nil, cleanup, err
			}
		}
		src, err := access.exact(n.Source, n.Commit)
		if err != nil {
			return nil, cleanup, err
		}
		dir := filepath.Join(root, n.Name)
		if err := src.Extract(n.Name, n.Commit, dir); err != nil {
			return nil, cleanup, fmt.Errorf("extract skill %s: %w", n.Name, err)
		}
		meta, err := source.ReadSkillMetadata(dir, n.Name)
		if err != nil {
			return nil, cleanup, fmt.Errorf("skill %s: %w", n.Name, err)
		}
		raw, err := dependency.NormalizeRequirements(meta.Requires, n.Source, false)
		if err != nil {
			return nil, cleanup, fmt.Errorf("skill %s: %w", n.Name, err)
		}
		if !slices.Equal(raw, n.Requires) {
			return nil, cleanup, fmt.Errorf("skill %s: resolved.requires snapshot disagrees with pinned frontmatter", n.Name)
		}
		sum, err := skill.Hash(dir)
		if err != nil {
			return nil, cleanup, err
		}
		if expected != "" && sum != expected {
			return nil, cleanup, fmt.Errorf("skill %s: hash mismatch (kitt.toml pins %s, source gives %s)", n.Name, expected, sum)
		}
		staged[n.Name] = stagedSkill{dir: dir, hash: sum}
	}
	return staged, cleanup, nil
}

func preflightInstall(p paths, plan *dependency.Plan) error {
	for _, dir := range []string{p.skillsDir, p.claudeDir, filepath.Dir(p.manifest)} {
		if err := preflightDirectory(dir); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(plan.Nodes)+1)
	for _, n := range plan.Nodes {
		names = append(names, n.Name)
	}
	if !p.global {
		agents, err := os.ReadFile(filepath.Join(p.root, "AGENTS.md"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read AGENTS.md: %w", err)
		}
		active := false
		for _, line := range strings.Split(string(agents), "\n") {
			active = active || strings.Trim(line, " \r") == templates.BootstrapMarker
		}
		if active {
			if err := p.installed.check(templates.BootstrapSkill); err != nil {
				return err
			}
			names = append(names, templates.BootstrapSkill)
		}
		for _, name := range []string{"CLAUDE.md", ".gitignore"} {
			if _, err := os.ReadFile(filepath.Join(p.root, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("read %s: %w", name, err)
			}
		}
		if exactLineIndex(string(agents), templates.DocsStart) >= 0 {
			if _, err := docsIndex(filepath.Join(p.root, ".agents", "docs")); err != nil {
				return err
			}
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "kitt-") && name != templates.BootstrapSkill {
			return fmt.Errorf("skill %s: skill names starting with kitt- are reserved", name)
		}
		link := filepath.Join(p.claudeDir, name)
		if _, err := os.Lstat(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", link, err)
		}
		if err := preflightDestination(p, name); err != nil {
			return err
		}
	}
	return nil
}

// Also used before metadata extraction: Windows device names cannot even be
// staged, and must produce a destination-specific diagnostic before Git access.
func preflightDestination(p paths, name string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	abs, err := filepath.Abs(filepath.Join(p.skillsDir, name))
	if err != nil {
		return err
	}
	link, err := filepath.Abs(filepath.Join(p.claudeDir, name))
	if err != nil {
		return err
	}
	if err := validateJunctionPaths(link, abs); err != nil {
		return fmt.Errorf("skill %s, link %s to %s: %w", name, link, abs, err)
	}
	for _, path := range []string{link, abs} {
		if len(utf16.Encode([]rune(path))) >= 260 {
			return fmt.Errorf("skill %s: install path %s is too long for kitt's Windows junction command; use a shorter install root", name, path)
		}
	}
	base := strings.ToUpper(name)
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return fmt.Errorf("skill %s: destination %s uses a Windows reserved device name", name, abs)
	}
	return nil
}

func preflightDirectory(dir string) error {
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("install path %s is not a directory", dir)
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat install path %s: %w", dir, err)
		}
		dir = filepath.Dir(dir)
	}
}

func applyStaged(p paths, plan *dependency.Plan, staged map[string]stagedSkill, tx *applyTransaction) error {
	nodes := map[string]dependency.Node{}
	for _, n := range plan.Nodes {
		nodes[n.Name] = n
	}
	done := map[string]bool{}
	var apply func(string) error
	apply = func(name string) error {
		if done[name] {
			return nil
		}
		done[name] = true // BuildPlan already ruled out cycles.
		for _, text := range nodes[name].Requires {
			r, _ := dependency.ParseSnapshotRef(text)
			if err := apply(r.Name); err != nil {
				return err
			}
		}
		if s := staged[name]; s.dir != "" {
			if err := os.MkdirAll(p.skillsDir, 0o755); err != nil {
				return err
			}
			dest := filepath.Join(p.skillsDir, name)
			if err := tx.moveAside(dest); err != nil {
				return err
			}
			// Persist ownership before publishing a directory, including on failure.
			if err := p.installed.add(name); err != nil {
				return err
			}
			if err := applyStep("rename", dest); err != nil {
				return err
			}
			if err := os.Rename(s.dir, dest); err != nil {
				return fmt.Errorf("install skill %s: %w", name, err)
			}
		}
		return p.installed.add(name)
	}
	for _, n := range plan.Nodes {
		if err := apply(n.Name); err != nil {
			return err
		}
	}
	return nil
}

// applyStep is a per-run test seam; production callers leave it unset.
var applyTestHook func(step, path string) error

func applyStep(step, path string) error {
	if applyTestHook != nil {
		return applyTestHook(step, path)
	}
	return nil
}

// Only abandoned staging trees are swept. Backups are retained on rollback
// failure and must never be swept automatically.
func sweepStaging(dir string, started time.Time) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), tempPrefix+"plan-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(started) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

type savedFile struct {
	path   string
	data   []byte
	mode   fs.FileMode
	target string
	exists bool
}

type movedPath struct{ path, backup string }

type applyTransaction struct {
	root            string
	files           []savedFile
	moved           []movedPath
	parents         []string
	state           *installedState
	names           map[string]bool
	dirty, deferred bool
}

func newApplyTransaction(p paths) (*applyTransaction, error) {
	root, err := os.MkdirTemp(p.skillsDir, tempPrefix+"backup-")
	if err != nil {
		return nil, err
	}
	tx := &applyTransaction{root: root, state: p.installed, names: map[string]bool{}, dirty: p.installed.dirty, deferred: p.installed.deferred}
	for name, owned := range p.installed.names {
		tx.names[name] = owned
	}
	for _, dir := range []string{p.claudeDir, filepath.Dir(p.claudeDir), filepath.Dir(p.manifest)} {
		if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			tx.parents = append(tx.parents, dir)
		}
	}
	files := []string{p.manifest, filepath.Join(p.skillsDir, installedFile)}
	if !p.global {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".gitignore"} {
			files = append(files, filepath.Join(p.root, name))
		}
	}
	seen := map[string]bool{}
	for _, path := range files {
		// Finish writes through project-file symlinks. Preserve both each link
		// and its referent so rollback also restores the bytes reached through it.
		for !seen[path] {
			seen[path] = true
			f := savedFile{path: path}
			info, err := os.Lstat(path)
			if err == nil {
				f.exists, f.mode = true, info.Mode()
				if info.Mode()&os.ModeSymlink != 0 {
					f.target, err = os.Readlink(path)
				} else {
					f.data, err = os.ReadFile(path)
				}
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				os.RemoveAll(root)
				return nil, err
			}
			tx.files = append(tx.files, f)
			if f.mode&os.ModeSymlink == 0 {
				break
			}
			path = f.target
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(f.path), path)
			}
		}
	}
	return tx, nil
}

// Record the destination before attempting either rename. A missing destination
// is also recorded, so rollback removes newly published directories and links.
func (tx *applyTransaction) moveAside(path string) error {
	for _, moved := range tx.moved {
		if moved.path == path {
			return nil
		}
	}
	moved := movedPath{path: path}
	if _, err := os.Lstat(path); err == nil {
		moved.backup = filepath.Join(tx.root, fmt.Sprintf("%d", len(tx.moved)))
		if err := os.Rename(path, moved.backup); err != nil {
			return fmt.Errorf("back up %s to %s: %w", path, moved.backup, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tx.moved = append(tx.moved, moved)
	return nil
}

func (tx *applyTransaction) prepareFinish(p paths, names []string, prune bool, out io.Writer) error {
	// Keep managed links as filesystem objects, including Windows junctions.
	for _, name := range names {
		path := filepath.Join(p.claudeDir, name)
		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if err := tx.moveAside(path); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	for name := range p.installed.names {
		if !p.global && !slices.Contains(names, name) {
			path := filepath.Join(p.claudeDir, name)
			target, err := os.Readlink(path)
			if err == nil {
				abs, err := absoluteLinkTarget(p.claudeDir, target)
				skills, _ := filepath.Abs(p.skillsDir)
				if err == nil && withinDir(skills, abs) {
					if err := tx.moveAside(path); err != nil {
						return err
					}
				}
			}
			if prune && name != templates.BootstrapSkill {
				dest := filepath.Join(p.skillsDir, name)
				_, statErr := os.Lstat(dest)
				if err := tx.moveAside(dest); err != nil {
					return err
				}
				if statErr == nil {
					fmt.Fprintf(out, "removed %s\n", name)
				}
			}
		}
	}
	if !p.global {
		data, err := os.ReadFile(filepath.Join(p.root, "AGENTS.md"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		active := false
		for _, line := range strings.Split(string(data), "\n") {
			active = active || strings.Trim(line, " \r") == templates.BootstrapMarker
		}
		if active || p.installed.names[templates.BootstrapSkill] {
			link := filepath.Join(p.claudeDir, templates.BootstrapSkill)
			dest := filepath.Join(p.skillsDir, templates.BootstrapSkill)
			if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) {
				if err := tx.moveAside(link); err != nil {
					return err
				}
			}
			if err := tx.moveAside(dest); err != nil {
				return err
			}
			if active {
				if err := p.installed.add(templates.BootstrapSkill); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func restoreSavedFile(f savedFile) error {
	info, err := os.Lstat(f.path)
	if !f.exists {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return os.Remove(f.path)
	}
	if err == nil && info.Mode() == f.mode {
		if f.mode&os.ModeSymlink != 0 {
			if target, err := os.Readlink(f.path); err == nil && target == f.target {
				return nil
			}
		} else if data, err := os.ReadFile(f.path); err == nil && string(data) == string(f.data) {
			return nil
		}
	}
	if f.mode&os.ModeSymlink != 0 {
		if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.Symlink(f.target, f.path)
	}
	// Rename also restores a read-only original without writing through it.
	file, err := os.CreateTemp(filepath.Dir(f.path), tempPrefix+"rollback-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(f.data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(file.Name(), f.mode.Perm()); err != nil {
		return err
	}
	return os.Rename(file.Name(), f.path)
}

func (tx *applyTransaction) rollback() error {
	var failures []string
	for i := len(tx.moved) - 1; i >= 0; i-- {
		moved := tx.moved[i]
		err := applyStep("rollback", moved.path)
		if err == nil {
			err = removeTree(moved.path)
		}
		if err == nil && moved.backup != "" {
			err = os.Rename(moved.backup, moved.path)
		}
		if err != nil {
			if moved.backup == "" {
				failures = append(failures, fmt.Sprintf("%s: %v", moved.path, err))
			} else {
				failures = append(failures, fmt.Sprintf("%s (backup %s): %v", moved.path, moved.backup, err))
			}
		}
	}
	for _, file := range tx.files {
		if err := restoreSavedFile(file); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", file.path, err))
		}
	}
	tx.state.names, tx.state.dirty, tx.state.deferred = tx.names, tx.dirty, tx.deferred
	for _, dir := range tx.parents {
		_ = os.Remove(dir)
	}
	if len(failures) > 0 {
		return fmt.Errorf("rollback incomplete; manual attention required: %s; backups retained in %s", strings.Join(failures, "; "), tx.root)
	}
	if err := removeTree(tx.root); err != nil {
		return fmt.Errorf("rollback restored managed files; manual attention required for backup directory %s: %w", tx.root, err)
	}
	return nil
}

// removeTree removes links (including junctions) as directory entries. It never
// walks a junction target, even if a platform reports the junction as a directory.
func removeTree(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Readlink(path); err == nil {
		return os.Remove(path)
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := removeTree(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	return os.Remove(path)
}
