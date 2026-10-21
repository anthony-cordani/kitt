package install

import (
	"os/exec"
	"syscall"
)

func configureJunctionCommand(cmd *exec.Cmd, link, target string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: junctionCommandLine(link, target)}
}
