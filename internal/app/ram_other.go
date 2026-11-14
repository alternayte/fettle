//go:build !darwin && !linux

package app

func hostRAMBytes() int64 { return 0 }
