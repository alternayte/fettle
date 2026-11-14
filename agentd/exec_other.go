//go:build !unix

package agentd

import (
	"os"
	"os/exec"
	"syscall"
)

func groupAttr() *syscall.SysProcAttr { return nil }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	return ps.ExitCode()
}
