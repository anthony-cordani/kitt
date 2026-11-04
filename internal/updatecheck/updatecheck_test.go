package updatecheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const testHint = "brew upgrade --cask anthony-cordani/tap/kitt"

var testNow = time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)

type fixture struct {
	checker  *Checker
	out      bytes.Buffer
	requests atomic.Int32
}

func newFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	f := &fixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	f.checker = &Checker{
		Current:   "0.4.0",
		URL:       server.URL,
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
		Hint:      testHint,
		Out:       &f.out,
		Now:       func() time.Time { return testNow },
	}
	return f
}

func releaseHandler(tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	}
}

func seedCache(t *testing.T, f *fixture, checkedAt time.Time, latest string) {
	t.Helper()
	data, err := json.Marshal(cached{CheckedAt: checkedAt, Latest: latest})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.checker.CachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertCache(t *testing.T, f *fixture, checkedAt time.Time, latest string) {
	t.Helper()
	data, err := os.ReadFile(f.checker.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	var got cached
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Latest != latest || !got.CheckedAt.Equal(checkedAt) {
		t.Errorf("cache = %+v, want latest %q checked at %s", got, latest, checkedAt)
	}
}

func assertOutput(t *testing.T, f *fixture, latest string) {
	t.Helper()
	want := ""
	if latest != "" {
		want = fmt.Sprintf("Kitt update available 0.4.0 -> %s (run: %s)\n", latest, testHint)
	}
	if got := f.out.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func assertRequests(t *testing.T, f *fixture, want int32) {
	t.Helper()
	if got := f.requests.Load(); got != want {
		t.Errorf("requests = %d, want %d", got, want)
	}
}

func TestNewerRelease(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "kitt/0.4.0" {
			t.Errorf("unexpected request: method %s, headers %v", r.Method, r.Header)
		}
		releaseHandler("v0.5.0")(w, r)
	})
	finish := f.checker.Start()
	assertOutput(t, f, "")
	finish()
	assertOutput(t, f, "0.5.0")
	assertRequests(t, f, 1)
	assertCache(t, f, testNow, "0.5.0")
}

func TestSameOrOlderRelease(t *testing.T) {
	for _, tag := range []string{"v0.4.0", "v0.3.0"} {
		t.Run(tag, func(t *testing.T) {
			f := newFixture(t, releaseHandler(tag))
			f.checker.Start()()
			assertOutput(t, f, "")
			assertRequests(t, f, 1)
		})
	}
}

func TestFreshCache(t *testing.T) {
	f := newFixture(t, releaseHandler("v0.6.0"))
	checkedAt := testNow.Add(-time.Hour)
	seedCache(t, f, checkedAt, "0.5.0")
	f.checker.Start()()
	assertOutput(t, f, "0.5.0")
	assertRequests(t, f, 0)
	assertCache(t, f, checkedAt, "0.5.0")
}

func TestStaleCache(t *testing.T) {
	f := newFixture(t, releaseHandler("v0.6.0"))
	seedCache(t, f, testNow.Add(-25*time.Hour), "0.5.0")
	f.checker.Start()()
	assertOutput(t, f, "0.6.0")
	assertRequests(t, f, 1)
	assertCache(t, f, testNow, "0.6.0")
}

func TestFutureCache(t *testing.T) {
	f := newFixture(t, releaseHandler("v0.6.0"))
	seedCache(t, f, testNow.Add(time.Hour), "0.5.0")
	f.checker.Start()()
	assertOutput(t, f, "0.6.0")
	assertRequests(t, f, 1)
	assertCache(t, f, testNow, "0.6.0")
}

func TestCorruptCache(t *testing.T) {
	f := newFixture(t, releaseHandler("v0.5.0"))
	if err := os.WriteFile(f.checker.CachePath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.checker.Start()()
	assertOutput(t, f, "0.5.0")
	assertRequests(t, f, 1)
	assertCache(t, f, testNow, "0.5.0")
}

func TestFailedRefresh(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"rate limit", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }},
		{"server error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
		{"invalid JSON", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{broken") }},
		{"prerelease", releaseHandler("v0.5.0-rc1")},
		{"terminal escape", releaseHandler("\x1b[31m0.9.9")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, latest := range []string{"", "0.4.0", "0.5.0"} {
				name := latest
				if name == "" {
					name = "no cache"
				}
				t.Run(name, func(t *testing.T) {
					f := newFixture(t, tc.handler)
					if latest != "" {
						seedCache(t, f, testNow.Add(-25*time.Hour), latest)
					}
					wantNotice := ""
					if latest == "0.5.0" {
						wantNotice = latest
					}
					f.checker.Start()()
					assertOutput(t, f, wantNotice)
					assertRequests(t, f, 1)
					assertCache(t, f, testNow, latest)

					f.out.Reset()
					f.checker.Now = func() time.Time { return testNow.Add(23 * time.Hour) }
					f.checker.Start()()
					assertOutput(t, f, wantNotice)
					assertRequests(t, f, 1)
					assertCache(t, f, testNow, latest)
				})
			}
		})
	}
}

func TestSlowServer(t *testing.T) {
	release := make(chan struct{})
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		releaseHandler("v0.6.0")(w, r)
	})
	seedCache(t, f, testNow.Add(-25*time.Hour), "0.5.0")
	finish := f.checker.Start()
	// Registered after server cleanup so the blocked handlers are released first.
	t.Cleanup(func() {
		close(release)
		finish()
	})
	started := time.Now()
	finish()
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Errorf("finish took %s, want less than 2s", elapsed)
	}
	assertOutput(t, f, "0.5.0")
	assertRequests(t, f, 1)
	assertCache(t, f, testNow, "0.5.0")

	f.out.Reset()
	f.checker.Start()()
	assertOutput(t, f, "0.5.0")
	assertRequests(t, f, 1)
}

func TestUnwritableCache(t *testing.T) {
	f := newFixture(t, releaseHandler("v0.5.0"))
	parent := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.checker.CachePath = filepath.Join(parent, "update-check.json")
	f.checker.Start()()
	assertOutput(t, f, "0.5.0")
	assertRequests(t, f, 1)
}

func TestHintFor(t *testing.T) {
	cases := []struct {
		name        string
		exe         string
		goInstalled bool
		want        string
	}{
		{"brew cask", filepath.FromSlash("/opt/homebrew/Caskroom/kitt/0.4.0/kitt"), false, testHint},
		{"brew cellar", filepath.FromSlash("/usr/local/Cellar/kitt/0.4.0/bin/kitt"), false, testHint},
		{"scoop", filepath.FromSlash("/Users/x/scoop/apps/kitt/current/kitt.exe"), false, "scoop update kitt"},
		{"winget", filepath.FromSlash("/Users/x/AppData/Local/Microsoft/WinGet/Packages/kitt/kitt.exe"), false, "winget upgrade AnthonyCordani.kitt"},
		// FromSlash uses backslashes on Windows and native separators on other OSes.
		{"windows scoop", filepath.FromSlash("C:/Users/x/scoop/apps/kitt/current/kitt.exe"), false, "scoop update kitt"},
		{"windows winget", filepath.FromSlash("C:/Users/x/AppData/Local/Microsoft/WinGet/Packages/kitt/kitt.exe"), false, "winget upgrade AnthonyCordani.kitt"},
		{"go install", filepath.Join("home", "x", "go", "bin", "kitt"), true, "go install github.com/anthony-cordani/kitt/cmd/kitt@latest"},
		{"go install priority", filepath.FromSlash("/opt/homebrew/Cellar/kitt/bin/kitt"), true, "go install github.com/anthony-cordani/kitt/cmd/kitt@latest"},
		{"unknown", filepath.FromSlash("/home/x/.local/bin/kitt"), false, releasesPage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hintFor(tc.exe, tc.goInstalled); got != tc.want {
				t.Errorf("hintFor(%q, %t) = %q, want %q", tc.exe, tc.goInstalled, got, tc.want)
			}
		})
	}
}

func TestDefaultDisabled(t *testing.T) {
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	cases := []struct {
		name    string
		version string
		ci      string
		optOut  string
	}{
		{"CI", "0.4.0", "1", ""},
		{"opt out", "0.4.0", "", "1"},
		{"development", "dev", "", ""},
		{"pseudo version", "v0.0.0-20261104120000-0123456789ab", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			t.Setenv("KITT_NO_UPDATE_CHECK", tc.optOut)
			if got := Default(tc.version, false, out); got != nil {
				t.Errorf("Default(%q) = %+v, want nil", tc.version, got)
			}
		})
	}
	t.Run("regular file", func(t *testing.T) {
		t.Setenv("CI", "")
		t.Setenv("KITT_NO_UPDATE_CHECK", "")
		out, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = out.Close() })
		if got := Default("0.4.0", false, out); got != nil {
			t.Errorf("Default with a regular file = %+v, want nil", got)
		}
	})
}
