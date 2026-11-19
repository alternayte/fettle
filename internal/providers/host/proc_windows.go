//go:build windows

package host

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func groupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func terminateGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func killGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }

// inheritParent lets agentd inherit the handle of r and returns its number.
func inheritParent(cmd *exec.Cmd, r *os.File) string {
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, syscall.Handle(r.Fd()))
	return strconv.FormatUint(uint64(r.Fd()), 10)
}
