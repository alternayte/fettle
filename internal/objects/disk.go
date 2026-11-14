package objects

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Disk stores objects as files under a root folder.
type Disk struct{ Root string }

// NewDisk creates the root folder.
func NewDisk(root string) (*Disk, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Disk{Root: root}, nil
}

func (d *Disk) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	if clean == "/" || strings.Contains(key, "..") {
		return "", fmt.Errorf("objects: bad key %q", key)
	}
	return filepath.Join(d.Root, filepath.FromSlash(clean)), nil
}

func (d *Disk) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (d *Disk) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (d *Disk) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (d *Disk) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(d.Root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || strings.HasPrefix(e.Name(), ".put-") {
			return nil
		}
		rel, _ := filepath.Rel(d.Root, p)
		k := filepath.ToSlash(rel)
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
		return nil
	})
	return keys, err
}

func (d *Disk) Check(ctx context.Context) error {
	const k = ".health/check"
	if err := d.Put(ctx, k, bytes.NewReader([]byte("ok")), 2); err != nil {
		return fmt.Errorf("object store is not writable: %w", err)
	}
	return d.Delete(ctx, k)
}
