package install

import (
	"path/filepath"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestDoctorIgnoresGitRepositoryOverrides(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteFile(t, filepath.Join(repo, "file"), "body\n")
	sha := testutil.Commit(t, repo, "init")
	for _, key := range []string{"GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, filepath.Join(t.TempDir(), "missing"))
			if status := gitDiffStatus(repo, sha, []string{"file"}); status != gitSame {
				t.Fatalf("status = %d", status)
			}
		})
	}
}
