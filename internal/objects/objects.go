// Package objects stores workspaces, payloads and attachments on disk or in S3-compatible storage.
package objects

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound means the key does not exist.
var ErrNotFound = errors.New("object not found")

// Store is the object store. Keys are slash-separated paths.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// List returns the keys under prefix.
	List(ctx context.Context, prefix string) ([]string, error)
	// Check verifies that the store can write and read (for /readyz and doctor).
	Check(ctx context.Context) error
}
