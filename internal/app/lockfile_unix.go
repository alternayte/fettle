//go:build !windows

package app

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockFile takes an exclusive lock on path and waits until it gets it or ctx ends.
func lockFile(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("migration lock %s: %w", path, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
