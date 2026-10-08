package release

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runGit runs `git -C root` with args.
// The environment has no GIT_DIR, GIT_WORK_TREE, or GIT_INDEX_FILE, and GIT_TERMINAL_PROMPT=0.
// A non-zero exit returns an error that includes git's trimmed stderr.
func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		shown := append([]string{"-C", root}, args...)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(shown, " "), err)
		}
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(shown, " "), msg, err)
	}
	return stdout.String(), nil
}

func gitEnv() []string {
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
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

// diffQuiet reports whether path is identical between rev and HEAD.
// Git exits 1 when the path differs and 0 when it does not.
func diffQuiet(root, rev, path string) (bool, error) {
	_, err := runGit(root, "diff", "--quiet", rev, "HEAD", "--", path)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
