package agentd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alternayte/fettle/agentd/client"
)

// relPath validates a path from a request and returns it cleaned, in the
// local separator, relative to the home folder. It refuses absolute paths
// and paths that climb out with "..". Symlinks that leave the home folder
// are refused later by os.Root on each operation.
func relPath(q string, allowHome bool) (string, error) {
	if q == "" {
		if allowHome {
			return ".", nil
		}
		return "", badRequest("path is required")
	}
	if strings.HasPrefix(q, "/") || strings.HasPrefix(q, `\`) || filepath.IsAbs(q) || filepath.VolumeName(q) != "" {
		return "", errPathEscapes
	}
	p := filepath.Clean(filepath.FromSlash(q))
	if p == "." {
		if allowHome {
			return ".", nil
		}
		return "", badRequest("path must not be the home folder")
	}
	if !filepath.IsLocal(p) {
		return "", errPathEscapes
	}
	return p, nil
}

// mkdirAll creates dir and its parents. os.Root reports a symlink that
// leads out as an existing file; this reports it as an escape.
func mkdirAll(root *os.Root, dir string) error {
	err := root.MkdirAll(dir, 0o755)
	if err != nil {
		if _, serr := root.Stat(dir); isRootEscape(serr) {
			return serr
		}
	}
	return err
}

// fsPath converts a local relative path to the slash form that io/fs uses.
func fsPath(rel string) string { return filepath.ToSlash(rel) }

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("path")
	rel, err := relPath(q, false)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	f, err := s.root.Open(rel)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		s.fail(w, q, err)
		return
	}
	if st.IsDir() {
		writeError(w, http.StatusBadRequest, q+": path is a folder")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		s.logger.Warn("read file: copy failed", "path", q, "error", cleanMessage(err))
	}
}

func parseMode(q string) (os.FileMode, bool, error) {
	if q == "" {
		return 0, false, nil
	}
	m, err := strconv.ParseUint(q, 8, 32)
	if err != nil || m > 0o777 {
		return 0, false, badRequest("mode must be an octal permission such as 0644, got %q", q)
	}
	return os.FileMode(m), true, nil
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("path")
	rel, err := relPath(q, false)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	mode, hasMode, err := parseMode(r.URL.Query().Get("mode"))
	if err != nil {
		s.fail(w, q, err)
		return
	}
	if err := s.writeAtomic(rel, r.Body, mode, hasMode); err != nil {
		s.fail(w, q, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeAtomic writes src to rel through a temporary file in the same folder
// and a rename. Without a mode it keeps the mode of an existing file.
func (s *Server) writeAtomic(rel string, src io.Reader, mode os.FileMode, hasMode bool) error {
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := mkdirAll(s.root, dir); err != nil {
			return err
		}
	}
	st, err := s.root.Stat(rel)
	switch {
	case err == nil && st.IsDir():
		return &httpError{status: http.StatusBadRequest, msg: fsPath(rel) + ": path is a folder"}
	case err == nil && !hasMode:
		mode = st.Mode().Perm()
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	case !hasMode:
		mode = 0o644
	}
	tmp := filepath.Join(dir, ".fettle-tmp-"+randomID())
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = fillFile(f, src, mode)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.root.Rename(tmp, rel)
	}
	if err != nil {
		_ = s.root.Remove(tmp)
		return err
	}
	return nil
}

func fillFile(f *os.File, src io.Reader, mode os.FileMode) error {
	if _, err := io.Copy(f, src); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("path")
	rel, err := relPath(q, false)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	if _, err := s.root.Lstat(rel); err != nil {
		s.fail(w, q, err)
		return
	}
	if r.URL.Query().Get("recursive") == "1" {
		err = s.root.RemoveAll(rel)
	} else {
		err = s.root.Remove(rel)
	}
	if err != nil {
		s.fail(w, q, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func entryFor(p string, info fs.FileInfo) client.Entry {
	return client.Entry{
		Path:    p,
		Size:    info.Size(),
		Mode:    info.Mode().Perm(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime().UTC(),
	}
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("path")
	rel, err := relPath(q, true)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	st, err := s.root.Stat(rel)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	if !st.IsDir() {
		writeError(w, http.StatusBadRequest, q+": not a folder")
		return
	}
	base := fsPath(rel)
	fsys := s.root.FS()
	entries := []client.Entry{}
	if r.URL.Query().Get("recursive") == "1" {
		err = fs.WalkDir(fsys, base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == base {
					return err
				}
				return nil
			}
			if p == base {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, entryFor(p, info))
			return nil
		})
	} else {
		var des []fs.DirEntry
		des, err = fs.ReadDir(fsys, base)
		for _, d := range des {
			info, ierr := d.Info()
			if ierr != nil {
				continue
			}
			entries = append(entries, entryFor(path.Join(base, d.Name()), info))
		}
	}
	if err != nil {
		s.fail(w, q, err)
		return
	}
	writeJSON(w, http.StatusOK, client.ListReply{Entries: entries})
}

func (s *Server) statFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("path")
	rel, err := relPath(q, true)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	info, err := s.root.Stat(rel)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	writeJSON(w, http.StatusOK, entryFor(fsPath(rel), info))
}
