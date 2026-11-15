package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a consistent copy of the SQLite database (VACUUM INTO, which works while
// the server runs) and every object of the store into dir (§17). It returns the number of
// objects. With PostgreSQL it copies the objects only.
func (a *App) Backup(ctx context.Context, dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	if !a.DB.Postgres {
		dst := filepath.Join(dir, "fettle.db")
		if _, err := os.Stat(dst); err == nil {
			return 0, fmt.Errorf("%s exists; use an empty folder", dst)
		}
		if _, err := a.DB.Engine.Exec(ctx, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'"); err != nil {
			return 0, fmt.Errorf("copy the database: %w", err)
		}
	}
	keys, err := a.Objects.List(ctx, "")
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		p := filepath.Join(dir, "objects", filepath.FromSlash(k))
		if !strings.HasPrefix(p, filepath.Join(dir, "objects")+string(filepath.Separator)) {
			return 0, errors.New("an object key leaves the folder: " + k)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return 0, err
		}
		rc, err := a.Objects.Get(ctx, k)
		if err != nil {
			return 0, err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			_ = rc.Close()
			return 0, err
		}
		_, err = io.Copy(f, rc)
		_ = rc.Close()
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}
