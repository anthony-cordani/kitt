package source_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestDefaults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		want    []string
		wantErr bool
	}{
		{name: "absent"},
		{name: "valid", file: "skills = [\"adr\", \"code-review\"]\nunknown = true\n", want: []string{"adr", "code-review"}},
		{name: "invalid", file: "skills = [", wantErr: true},
		{name: "wrong type", file: "skills = 42", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			repo := testutil.NewRepo(t)
			testutil.WriteFile(t, filepath.Join(repo, "README.md"), "source\n")
			if tc.file != "" {
				testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), tc.file)
			}
			testutil.Commit(t, repo, "defaults")
			s := openSource(t, "demo", repo)
			got, err := s.Defaults()
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Defaults = %q, %v; want %q, error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestDefaultsWithoutCommit(t *testing.T) {
	testutil.IsolateHome(t)
	s := openSource(t, "demo", testutil.NewRepo(t))
	if err := s.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := s.Defaults()
	if got != nil || err != nil {
		t.Fatalf("Defaults = %q, %v; want nil, nil", got, err)
	}
}
