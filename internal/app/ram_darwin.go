package app

import "golang.org/x/sys/unix"

func hostRAMBytes() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(n)
}
