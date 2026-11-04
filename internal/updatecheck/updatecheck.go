// Package updatecheck tells a person at a terminal that a newer kitt release exists.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	releaseURL   = "https://api.github.com/repos/anthony-cordani/kitt/releases/latest"
	releasesPage = "https://github.com/anthony-cordani/kitt/releases/latest"
	interval     = 24 * time.Hour
	retryAfter   = time.Hour
	fetchTimeout = 3 * time.Second
	finishWait   = 300 * time.Millisecond
	maxBody      = 1 << 20
)

// Checker compares the running version with the latest release, using a cached answer.
type Checker struct {
	Current   string // running version, without a leading "v"
	URL       string // GitHub "latest release" endpoint
	CachePath string
	Hint      string // how to update, as shown in the notice
	Out       io.Writer
	Now       func() time.Time
	Wait      time.Duration // longest a command waits for a refresh
}

type cached struct {
	CheckedAt   time.Time `json:"checked_at"`             // last answer from GitHub
	AttemptedAt time.Time `json:"attempted_at,omitempty"` // last request sent, answered or not
	Latest      string    `json:"latest"`
}

// Default returns the Checker for this process, or nil when no notice must be shown.
func Default(version string, goInstalled bool, out *os.File) *Checker {
	current := strings.TrimPrefix(version, "v")
	if !releaseVersion(current) || os.Getenv("CI") != "" || os.Getenv("KITT_NO_UPDATE_CHECK") != "" || !isTerminal(out) {
		return nil
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	exe, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	return &Checker{
		Current:   current,
		URL:       releaseURL,
		CachePath: filepath.Join(root, "kitt", "update-check.json"),
		Hint:      hintFor(exe, goInstalled),
		Out:       out,
		Now:       time.Now,
		Wait:      finishWait,
	}
}

// Start refreshes a stale cache in the background. The returned function prints the
// notice, waiting at most c.Wait for that refresh.
func (c *Checker) Start() func() {
	known := c.read()
	now := c.Now()
	if within(now, known.CheckedAt, interval) || within(now, known.AttemptedAt, retryAfter) {
		return func() { c.notify(known.Latest) }
	}
	// Written first: a slow network or a short command must not retry on every call,
	// nor give up for the whole day when it exits before the answer.
	c.write(cached{CheckedAt: known.CheckedAt, AttemptedAt: now, Latest: known.Latest})
	done := make(chan string, 1)
	go func() {
		latest, err := c.fetch()
		if err != nil {
			done <- known.Latest
			return
		}
		c.write(cached{CheckedAt: now, AttemptedAt: now, Latest: latest})
		done <- latest
	}()
	return func() {
		select {
		case latest := <-done:
			c.notify(latest)
		case <-time.After(c.Wait):
			c.notify(known.Latest)
		}
	}
}

func (c *Checker) notify(latest string) {
	if !releaseVersion(latest) || semver.Compare("v"+latest, "v"+c.Current) <= 0 {
		return
	}
	fmt.Fprintf(c.Out, "Kitt update available %s -> %s (%s)\n", c.Current, latest, c.Hint)
}

// within reports whether at is a past moment less than d before now. A zero or future time is not.
func within(now, at time.Time, d time.Duration) bool {
	age := now.Sub(at)
	return !at.IsZero() && age >= 0 && age < d
}

func (c *Checker) fetch() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kitt/"+c.Current)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: %s", resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&release); err != nil {
		return "", err
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if !releaseVersion(latest) {
		return "", fmt.Errorf("latest release: unexpected tag %q", release.TagName)
	}
	return latest, nil
}

func (c *Checker) read() cached {
	var got cached
	data, err := os.ReadFile(c.CachePath)
	if err != nil || json.Unmarshal(data, &got) != nil {
		return cached{}
	}
	return got
}

// write replaces the cache atomically. Failures are ignored: the notice is optional.
func (c *Checker) write(value cached) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	dir := filepath.Dir(c.CachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, "update-check-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	if closeErr := tmp.Close(); writeErr != nil || closeErr != nil {
		return
	}
	_ = os.Rename(tmp.Name(), c.CachePath)
}

// releaseVersion accepts a plain X.Y.Z. A pre-release or pseudo-version has no release to compare.
func releaseVersion(v string) bool {
	sv := "v" + v
	return semver.IsValid(sv) && semver.Canonical(sv) == sv && semver.Prerelease(sv) == "" && semver.Build(sv) == ""
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// hintFor names the update command of the package manager that owns the executable.
func hintFor(exe string, goInstalled bool) string {
	path := strings.ToLower(filepath.ToSlash(exe))
	switch {
	case goInstalled:
		return "run: go install github.com/anthony-cordani/kitt/cmd/kitt@latest"
	case strings.Contains(path, "/caskroom/") || strings.Contains(path, "/cellar/"):
		return "run: brew upgrade anthony-cordani/tap/kitt"
	case strings.Contains(path, "/scoop/"):
		return "run: scoop update kitt"
	case strings.Contains(path, "/winget/"):
		return "run: winget upgrade AnthonyCordani.kitt"
	default:
		return "see " + releasesPage
	}
}
