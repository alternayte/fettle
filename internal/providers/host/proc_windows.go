//go:build windows

package host

import (
	"os/exec"
	"syscall"
)

func groupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func terminateGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func killGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }
