//go:build unix

package agentd

import (
	"os"
	"os/exec"
	"syscall"
)

// groupAttr puts the command in its own process group, so that a timeout
// kills the command and all of its children.
func groupAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// exitCode returns the exit status, or 128 plus the signal number when a
// signal ended the process, as a shell reports it.
func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
