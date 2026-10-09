package install

import (
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestAddSourcesNilOutput(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "review", "1.0.0", "body")
	testutil.Commit(t, repo, "add review")
	if err := AddSources(Options{Global: true}, map[string]string{"s": testutil.URL(repo)}); err != nil {
		t.Fatalf("AddSources: %v", err)
	}
}
