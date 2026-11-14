package app

import "golang.org/x/sys/unix"

func hostRAMBytes() int64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	return int64(si.Totalram) * int64(si.Unit)
}
