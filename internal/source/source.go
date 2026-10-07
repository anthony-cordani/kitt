// Package source caches a git repository of skills and resolves versions.
package source

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	kskill "github.com/anthony-cordani/kitt/internal/skill"
)

// Source is a git repository of skills, cached locally as a bare clone.
type Source struct {
	Alias string
	URL   string
	dir   string
}

// Resolved is a skill version pinned to a commit.
type Resolved struct {
	Version string // "1.4.0" (no "v"); "" when the source has no tag for this skill
	Commit  string // full commit SHA
}

// Open returns the source, cloning it into the cache on first use.
func Open(alias, url string) (*Source, error) {
	dir, err := cacheDir(url)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("stat source cache: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return nil, fmt.Errorf("create source cache: %w", err)
		}
		if _, err := runGit("clone", "--bare", "--quiet", "--", url, dir); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}
	return &Source{Alias: alias, URL: url, dir: dir}, nil
}

// Check returns an error when url is not a git repository git can reach.
func Check(url string) error {
	_, err := runGit("ls-remote", "--quiet", "--", url)
	return err
}

// Fetch updates the cached clone from the remote.
func (s *Source) Fetch() error {
	_, err := runGit("-C", s.dir, "fetch", "--quiet", "--prune", "--tags", "origin", "+refs/heads/*:refs/heads/*")
	return err
}

// HasCommit reports whether commit is present in the cache.
func (s *Source) HasCommit(commit string) bool {
	if !isCommit(commit) {
		return false
	}
	_, err := runGit("-C", s.dir, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// Versions returns the released versions of a skill, ascending, without the "v" prefix.
func (s *Source) Versions(skill string) ([]string, error) {
	if err := kskill.ValidName(skill); err != nil {
		return nil, err
	}
	out, err := runGit("-C", s.dir, "tag", "--list", skill+"/v*")
	if err != nil {
		return nil, err
	}
	prefix := skill + "/"
	var matched []string
	text := strings.TrimSpace(string(out))
	if text == "" {
		return []string{}, nil
	}
	for _, line := range strings.Split(text, "\n") {
		tag := strings.TrimSpace(line)
		if tag == "" || !strings.HasPrefix(tag, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(tag, prefix)
		if semver.IsValid(suffix) && semver.Prerelease(suffix) == "" {
			matched = append(matched, suffix)
		}
	}
	semver.Sort(matched)
	for i, v := range matched {
		matched[i] = strings.TrimPrefix(v, "v")
	}
	if matched == nil {
		matched = []string{}
	}
	return matched, nil
}

// Resolve picks the commit to install for a skill and a version constraint.
func (s *Source) Resolve(skill, constraint string) (Resolved, error) {
	versions, err := s.Versions(skill)
	if err != nil {
		return Resolved{}, err
	}
	c, err := parseConstraint(constraint)
	if err != nil {
		return Resolved{}, err
	}
	if c.latest {
		if len(versions) == 0 {
			return s.resolveDegraded(skill)
		}
		return s.pin(skill, versions[len(versions)-1])
	}
	version, ok := c.match(versions)
	if !ok {
		return Resolved{}, fmt.Errorf("no version of %s matches %s", skill, constraint)
	}
	return s.pin(skill, version)
}

// Extract writes the skill directory found at commit into dest, which must not exist.
func (s *Source) Extract(skill, commit, dest string) error {
	if err := kskill.ValidName(skill); err != nil {
		return err
	}
	if !isCommit(commit) {
		return fmt.Errorf("invalid commit %q", commit)
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("destination %s already exists", dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}
	out, err := runGit("-C", s.dir, "archive", "--format=tar", commit, "skills/"+skill)
	if err != nil {
		return err
	}
	parent := filepath.Dir(dest)
	if parent != dest {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create destination: %w", err)
		}
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("destination %s already exists", dest)
		}
		return fmt.Errorf("create destination: %w", err)
	}
	if err := unpackSkill(out, skill, dest); err != nil {
		os.RemoveAll(dest)
		return err
	}
	return nil
}

// cacheDir is UserCacheDir/kitt/sources/<first 16 hex chars of sha256(url)>.
func cacheDir(url string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache dir: %w", err)
	}
	sum := sha256.Sum256([]byte(url))
	id := hex.EncodeToString(sum[:])[:16]
	return filepath.Join(root, "kitt", "sources", id), nil
}

func (s *Source) resolveDegraded(skill string) (Resolved, error) {
	commit, err := s.revParse("HEAD^{commit}")
	if err != nil {
		return Resolved{}, err
	}
	if err := s.requireSkill(skill, commit); err != nil {
		return Resolved{}, err
	}
	return Resolved{Commit: commit}, nil
}

func (s *Source) pin(skill, version string) (Resolved, error) {
	commit, err := s.revParse("refs/tags/" + skill + "/v" + version + "^{commit}")
	if err != nil {
		return Resolved{}, err
	}
	if err := s.requireSkill(skill, commit); err != nil {
		return Resolved{}, err
	}
	return Resolved{Version: version, Commit: commit}, nil
}

func (s *Source) revParse(rev string) (string, error) {
	out, err := runGit("-C", s.dir, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("git rev-parse %s: empty result", rev)
	}
	return commit, nil
}

func (s *Source) requireSkill(skill, commit string) error {
	_, err := runGit("-C", s.dir, "cat-file", "-e", commit+":skills/"+skill+"/SKILL.md")
	if err != nil {
		return fmt.Errorf("skill %s not found in %s", skill, s.Alias)
	}
	return nil
}

// constraint is the accepted version grammar: latest, exact X.Y.Z, or ^X[.Y[.Z]].
type constraint struct {
	latest bool
	exact  string
	major  string
	floor  string // canonical floor with a leading v, caret only
	caret  bool
}

func parseConstraint(s string) (constraint, error) {
	if s == "" {
		return constraint{latest: true}, nil
	}
	raw := s
	caret := false
	if strings.HasPrefix(raw, "^") {
		caret = true
		raw = strings.TrimPrefix(raw, "^")
	}
	parts := strings.Split(raw, ".")
	if caret {
		if len(parts) < 1 || len(parts) > 3 {
			return constraint{}, fmt.Errorf("invalid version constraint %q", s)
		}
	} else if len(parts) != 3 {
		return constraint{}, fmt.Errorf("invalid version constraint %q", s)
	}
	for _, p := range parts {
		if !validNumber(p) {
			return constraint{}, fmt.Errorf("invalid version constraint %q", s)
		}
	}
	if !caret {
		return constraint{exact: s}, nil
	}
	major, minor, patch := parts[0], "0", "0"
	if len(parts) >= 2 {
		minor = parts[1]
	}
	if len(parts) == 3 {
		patch = parts[2]
	}
	return constraint{
		caret: true,
		major: major,
		floor: "v" + major + "." + minor + "." + patch,
	}, nil
}

func (c constraint) match(versions []string) (string, bool) {
	if !c.caret {
		for _, v := range versions {
			if v == c.exact {
				return v, true
			}
		}
		return "", false
	}
	var best string
	found := false
	for _, v := range versions {
		sv := "v" + v
		if semver.Major(sv) != "v"+c.major {
			continue
		}
		if semver.Compare(sv, c.floor) < 0 {
			continue
		}
		best = v
		found = true
	}
	return best, found
}

func validNumber(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func unpackSkill(archive []byte, skill, dest string) error {
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		// Skip symlinks, pax metadata, and every other non-content entry.
		// Git archive emits a pax global header before the tree.
		if hdr.Typeflag != tar.TypeDir && hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		rel, ok, err := skillRelative(hdr.Name, skill)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		clean := path.Clean(rel)
		if clean == "." {
			continue
		}
		target, err := safeTarget(dest, clean)
		if err != nil {
			return err
		}
		mode := fileMode(hdr.Mode)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create directory: %w", err)
			}
			if err := os.Chmod(target, mode); err != nil {
				return fmt.Errorf("set directory mode: %w", err)
			}
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create directory: %w", err)
			}
			if err := writeFile(target, tr, mode); err != nil {
				return err
			}
		}
	}
}

// skillRelative strips the skills/<skill>/ prefix.
// ok is false for the skill directory itself and its ancestor directories.
func skillRelative(name, skill string) (rel string, ok bool, err error) {
	trimmed := strings.TrimSuffix(name, "/")
	root := "skills/" + skill
	if trimmed == root || strings.HasPrefix(root, trimmed+"/") {
		return "", false, nil
	}
	prefix := root + "/"
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false, fmt.Errorf("archive entry %q is outside skill %s", name, skill)
	}
	return strings.TrimPrefix(trimmed, prefix), true, nil
}

// safeTarget joins rel onto dest and rejects a cleaned path that escapes dest.
func safeTarget(dest, clean string) (string, error) {
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.Contains(clean, `\`) || strings.ContainsRune(clean, 0) {
		return "", fmt.Errorf("archive path %q escapes destination", clean)
	}
	slashed := filepath.FromSlash(clean)
	if filepath.IsAbs(slashed) || filepath.VolumeName(slashed) != "" {
		return "", fmt.Errorf("archive path %q escapes destination", clean)
	}
	target := filepath.Join(dest, slashed)
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("resolve destination: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve archive path: %w", err)
	}
	rel, err := filepath.Rel(absDest, absTarget)
	if err != nil {
		return "", fmt.Errorf("resolve archive path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive path %q escapes destination", clean)
	}
	return target, nil
}

func fileMode(mode int64) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("write file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write file: %w", closeErr)
	}
	if err := os.Chmod(target, mode); err != nil {
		return fmt.Errorf("set file mode: %w", err)
	}
	return nil
}

// isCommit reports whether s is a full lowercase hexadecimal object name (SHA-1 or SHA-256).
func isCommit(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// runGit runs git with autocrlf and eol pinned so object bytes match on every OS.
func runGit(args ...string) ([]byte, error) {
	cmdArgs := make([]string, 0, len(args)+4)
	cmdArgs = append(cmdArgs, "-c", "core.autocrlf=false", "-c", "core.eol=lf", "-c", "protocol.ext.allow=never")
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("git", cmdArgs...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		// A git hook exports GIT_DIR and friends; they would redirect every command to the project repository.
		if strings.HasPrefix(e, "GIT_TERMINAL_PROMPT=") || strings.HasPrefix(e, "GIT_DIR=") ||
			strings.HasPrefix(e, "GIT_WORK_TREE=") || strings.HasPrefix(e, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(e, "GIT_OBJECT_DIRECTORY=") || strings.HasPrefix(e, "GIT_ALTERNATE_OBJECT_DIRECTORIES=") ||
			strings.HasPrefix(e, "GIT_COMMON_DIR=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), msg, err)
	}
	return stdout.Bytes(), nil
}
