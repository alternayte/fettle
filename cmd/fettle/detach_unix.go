//go:build !windows

package main

import "syscall"

func detach() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
