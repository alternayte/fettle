//go:build !windows

package host

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func groupAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func terminateGroup(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func killGroup(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

// inheritParent passes r to agentd as descriptor 3 and returns that number.
func inheritParent(cmd *exec.Cmd, r *os.File) string {
	cmd.ExtraFiles = append(cmd.ExtraFiles, r)
	return strconv.Itoa(2 + len(cmd.ExtraFiles))
}
