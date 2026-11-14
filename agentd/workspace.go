package agentd

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/alternayte/fettle/agentd/client"
)

// defaultExclude holds the base-name patterns that a workspace export,
// manifest and diff always leave out.
var defaultExclude = []string{".cache", "node_modules", ".venv", "__pycache__", "*.log"}

// browserCacheNames are Chromium cache folders, excluded inside .browser.
var browserCacheNames = map[string]bool{"Cache": true, "Code Cache": true, "GPUCache": true}

// excluder decides which workspace paths to leave out.
type excluder struct {
	names []string // matched against each path component
	paths []string // matched against each leading part of the path
}

func newExcluder(extra []string) (*excluder, error) {
	e := &excluder{names: append([]string{}, defaultExclude...)}
	for _, p := range extra {
		p = strings.Trim(path.Clean("/"+strings.TrimSpace(p)), "/")
		if p == "" {
			continue
		}
		if _, err := path.Match(p, ""); err != nil {
			return nil, badRequest("exclude pattern %q is not valid", p)
		}
		if strings.Contains(p, "/") {
			e.paths = append(e.paths, p)
		} else {
			e.names = append(e.names, p)
		}
	}
	return e, nil
}

// excluded reports whether rel, a slash path relative to the workspace
// folder, is left out. A pattern without a slash matches any component; a
// pattern with a slash matches the path or a folder above it.
func (e *excluder) excluded(rel string) bool {
	comps := strings.Split(rel, "/")
	for _, c := range comps {
		for _, n := range e.names {
			if ok, _ := path.Match(n, c); ok {
				return true
			}
		}
	}
	for k := 1; k <= len(comps); k++ {
		lead := strings.Join(comps[:k], "/")
		for _, p := range e.paths {
			if ok, _ := path.Match(p, lead); ok {
				return true
			}
		}
	}
	for i, c := range comps {
		if c != ".browser" {
			continue
		}
		for j := i + 1; j < len(comps); j++ {
			if browserCacheNames[comps[j]] {
				return true
			}
			if comps[j] == "Service Worker" && j+1 < len(comps) && comps[j+1] == "CacheStorage" {
				return true
			}
		}
	}
	return false
}

// workspaceDir validates a workspace path and checks that it is a folder.
func (s *Server) workspaceDir(q string) (string, error) {
	rel, err := relPath(q, false)
	if err != nil {
		return "", err
	}
	st, err := s.root.Stat(rel)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", badRequest("%s: not a folder", q)
	}
	return rel, nil
}

// walkWorkspace calls fn for each entry under rel that is not excluded. sub
// is the slash path relative to rel and name is the path for os.Root.
func (s *Server) walkWorkspace(rel string, ex *excluder, fn func(sub, name string, d fs.DirEntry) error) error {
	base := fsPath(rel)
	return fs.WalkDir(s.root.FS(), base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == base {
			return nil
		}
		sub := strings.TrimPrefix(p, base+"/")
		if ex.excluded(sub) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		return fn(sub, filepath.FromSlash(p), d)
	})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	var req client.WorkspaceRequest
	if err := decodeJSON(w, r, 1<<20, &req); err != nil {
		s.fail(w, "", err)
		return
	}
	rel, err := s.workspaceDir(req.Path)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	ex, err := newExcluder(req.Exclude)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	w.Header().Set("Content-Type", "application/zstd")
	w.WriteHeader(http.StatusOK)
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		s.logger.Error("export: zstd writer", "error", err.Error())
		panic(http.ErrAbortHandler)
	}
	tw := tar.NewWriter(zw)
	err = s.walkWorkspace(rel, ex, func(sub, name string, d fs.DirEntry) error {
		return s.addTarEntry(tw, sub, name, d)
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		// The status is already sent. Abort the connection so that the
		// caller sees a broken stream, not a short archive.
		s.logger.Error("export failed", "path", req.Path, "error", cleanMessage(err))
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) addTarEntry(tw *tar.Writer, sub, name string, d fs.DirEntry) error {
	info, err := d.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    sub,
		Mode:    int64(info.Mode().Perm()),
		ModTime: info.ModTime(),
		Format:  tar.FormatPAX,
	}
	switch {
	case info.IsDir():
		hdr.Typeflag = tar.TypeDir
		hdr.Name += "/"
		return tw.WriteHeader(hdr)
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := s.root.Readlink(name)
		if err != nil {
			return err
		}
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = target
		return tw.WriteHeader(hdr)
	case info.Mode().IsRegular():
		f, err := s.root.Open(name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		hdr.Typeflag = tar.TypeReg
		hdr.Size = st.Size()
		hdr.Mode = int64(st.Mode().Perm())
		hdr.ModTime = st.ModTime()
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.CopyN(tw, f, hdr.Size); err != nil {
			return fmt.Errorf("%s: %w", sub, err)
		}
		return nil
	}
	return nil
}

func (s *Server) importWorkspace(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("path")
	rel, err := relPath(q, false)
	if err != nil {
		s.fail(w, q, err)
		return
	}
	var keepMode os.FileMode = 0o755
	st, err := s.root.Lstat(rel)
	switch {
	case err == nil && !st.IsDir():
		writeError(w, http.StatusBadRequest, q+": not a folder")
		return
	case err == nil:
		keepMode = st.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		s.fail(w, q, err)
		return
	}
	parent := filepath.Dir(rel)
	if parent != "." {
		if err := mkdirAll(s.root, parent); err != nil {
			s.fail(w, q, err)
			return
		}
	}
	id := randomID()
	tmp := filepath.Join(parent, ".fettle-import-"+id)
	if err := s.root.Mkdir(tmp, 0o700); err != nil {
		s.fail(w, q, err)
		return
	}
	if err := s.extract(tmp, r.Body); err != nil {
		_ = s.root.RemoveAll(tmp)
		s.fail(w, q, err)
		return
	}
	if err := s.root.Chmod(tmp, keepMode|0o700); err != nil {
		_ = s.root.RemoveAll(tmp)
		s.fail(w, q, err)
		return
	}
	if err := s.swap(rel, tmp, filepath.Join(parent, ".fettle-old-"+id), st != nil); err != nil {
		_ = s.root.RemoveAll(tmp)
		s.fail(w, q, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// swap moves tmp into place at rel and removes the old folder.
func (s *Server) swap(rel, tmp, old string, exists bool) error {
	if !exists {
		return s.root.Rename(tmp, rel)
	}
	if err := s.root.Rename(rel, old); err != nil {
		return err
	}
	if err := s.root.Rename(tmp, rel); err != nil {
		if rerr := s.root.Rename(old, rel); rerr != nil {
			s.logger.Error("import: restore of the old folder failed", "path", fsPath(rel), "error", cleanMessage(rerr))
		}
		return err
	}
	if err := s.root.RemoveAll(old); err != nil {
		s.logger.Warn("import: removal of the old folder failed", "path", fsPath(old), "error", cleanMessage(err))
	}
	return nil
}

// extract unpacks a tar stream compressed with zstd into dir. Every write
// goes through an os.Root at dir, so no entry and no symlink can lead out.
func (s *Server) extract(dir string, body io.Reader) error {
	zr, err := zstd.NewReader(body, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(128<<20))
	if err != nil {
		return badRequest("body is not zstd: %s", err.Error())
	}
	defer zr.Close()
	troot, err := s.root.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = troot.Close() }()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return badRequest("archive is not a valid tar with zstd: %s", err.Error())
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == "." {
			continue
		}
		local := filepath.FromSlash(name)
		if strings.HasPrefix(name, "/") || !filepath.IsLocal(local) {
			return &httpError{status: http.StatusForbidden, msg: fmt.Sprintf("archive entry %q escapes the folder", hdr.Name)}
		}
		if err := extractEntry(troot, tr, hdr, local); err != nil {
			if isRootEscape(err) {
				return &httpError{status: http.StatusForbidden, msg: fmt.Sprintf("archive entry %q escapes the folder", hdr.Name)}
			}
			return err
		}
	}
}

func extractEntry(troot *os.Root, tr *tar.Reader, hdr *tar.Header, local string) error {
	perm := os.FileMode(hdr.Mode).Perm()
	if parent := filepath.Dir(local); parent != "." {
		if err := mkdirAll(troot, parent); err != nil {
			return err
		}
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := mkdirAll(troot, local); err != nil {
			return err
		}
		return troot.Chmod(local, perm|0o700)
	case tar.TypeReg:
		f, err := troot.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		err = fillFile(f, tr, perm)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	case tar.TypeSymlink:
		return troot.Symlink(hdr.Linkname, local)
	}
	return badRequest("archive entry %q has an unsupported type", hdr.Name)
}

// manifestOf returns the SHA-256 hex of each regular file under rel.
func (s *Server) manifestOf(rel string, ex *excluder) (map[string]string, map[string]string, error) {
	files := map[string]string{}
	names := map[string]string{}
	err := s.walkWorkspace(rel, ex, func(sub, name string, d fs.DirEntry) error {
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := s.root.Open(name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		files[sub] = hex.EncodeToString(h.Sum(nil))
		names[sub] = name
		return nil
	})
	return files, names, err
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	var req client.WorkspaceRequest
	if err := decodeJSON(w, r, 1<<20, &req); err != nil {
		s.fail(w, "", err)
		return
	}
	rel, err := s.workspaceDir(req.Path)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	ex, err := newExcluder(req.Exclude)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	files, _, err := s.manifestOf(rel, ex)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	writeJSON(w, http.StatusOK, client.Manifest{Files: files})
}

func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	var req client.DiffRequest
	if err := decodeJSON(w, r, 64<<20, &req); err != nil {
		s.fail(w, "", err)
		return
	}
	rel, err := s.workspaceDir(req.Path)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	ex, err := newExcluder(nil)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	files, names, err := s.manifestOf(rel, ex)
	if err != nil {
		s.fail(w, req.Path, err)
		return
	}
	out := client.Diff{Added: []client.DiffFile{}, Changed: []client.DiffFile{}, Deleted: []string{}}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		baseHash, inBase := req.Base[k]
		if inBase && baseHash == files[k] {
			continue
		}
		content, err := s.root.ReadFile(names[k])
		if err != nil {
			s.fail(w, req.Path, err)
			return
		}
		f := client.DiffFile{Path: k, Content: content}
		if inBase {
			out.Changed = append(out.Changed, f)
		} else {
			out.Added = append(out.Added, f)
		}
	}
	for k := range req.Base {
		if _, ok := files[k]; !ok {
			out.Deleted = append(out.Deleted, k)
		}
	}
	sort.Strings(out.Deleted)
	writeJSON(w, http.StatusOK, out)
}
