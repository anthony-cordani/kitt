//go:build !windows

package install

import "os/exec"

func configureJunctionCommand(cmd *exec.Cmd, link, target string) {}
