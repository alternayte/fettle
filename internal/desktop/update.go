package desktop

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DefaultFeed is the release feed of signed update packages (§15.1).
const DefaultFeed = "https://github.com/alternayte/fettle/releases/latest/download/update.json"

// Manifest describes one update package for one platform.
type Manifest struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	// Packages maps GOOS-GOARCH (or GOOS-universal) to a package.
	Packages map[string]Package `json:"packages"`
}

// Package is one signed update file.
type Package struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// Signature is Ed25519 over "fettle-update\n<version>\n<os-arch>\n<sha256>", base64.
	Signature string `json:"signature"`
}

// Errors of updates.
var (
	ErrNoPackage = errors.New("the release has no package for this computer")
	ErrSignature = errors.New("the update package is not signed by Fettle")
	ErrChecksum  = errors.New("the update package does not match its checksum")
)

// Platform is the key of this computer in a manifest.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// signed is the message that a package signature covers.
func signed(version, platform, sum string) []byte {
	return []byte("fettle-update\n" + version + "\n" + platform + "\n" + strings.ToLower(sum))
}

// Sign signs a package (the release pipeline and tests use it).
func Sign(priv ed25519.PrivateKey, version, platform, sum string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signed(version, platform, sum)))
}

// Newer reports whether version a is newer than b (vMAJOR.MINOR.PATCH, a pre-release is
// older than its release).
func Newer(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	if pa.pre == pb.pre {
		return false
	}
	if pa.pre == "" {
		return true
	}
	if pb.pre == "" {
		return false
	}
	return pa.pre > pb.pre
}

type version struct {
	n   [3]int
	pre string
}

func parseVersion(s string) version {
	var v version
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, _ := strings.Cut(s, "-")
	v.pre = pre
	for i, p := range strings.SplitN(core, ".", 3) {
		v.n[i], _ = strconv.Atoi(p)
	}
	return v
}

// Updater checks the feed, downloads and verifies packages, and installs them.
type Updater struct {
	Feed    string
	Current string
	Key     ed25519.PublicKey
	HTTP    *http.Client
}

// Check returns the newer release for this computer, or nil.
func (u *Updater) Check(ctx context.Context) (*Manifest, *Package, error) {
	if len(u.Key) != ed25519.PublicKeySize {
		return nil, nil, nil // a build without a release key does not update
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.Feed, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("the update feed answers %d", resp.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, nil, err
	}
	if !Newer(m.Version, u.Current) {
		return nil, nil, nil
	}
	p, ok := m.Packages[Platform()]
	if !ok {
		if p, ok = m.Packages[runtime.GOOS+"-universal"]; !ok {
			return &m, nil, ErrNoPackage
		}
	}
	return &m, &p, nil
}

// Download fetches a package into dir and verifies its checksum and signature before it
// returns the file.
func (u *Updater) Download(ctx context.Context, m *Manifest, p *Package, dir string) (string, error) {
	platform := Platform()
	if _, ok := m.Packages[platform]; !ok {
		platform = runtime.GOOS + "-universal"
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil || !ed25519.Verify(u.Key, signed(m.Version, platform, p.SHA256), sig) {
		return "", ErrSignature
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the update package answers %d", resp.StatusCode)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "update-*.pkg")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 2<<30)); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), p.SHA256) {
		_ = os.Remove(f.Name())
		return "", ErrChecksum
	}
	return f.Name(), nil
}

// Install replaces the installed app with a verified package. target is the app bundle
// (macOS: the .app folder) or the program file (Linux AppImage). The package is a
// tar.gz with the new bundle at its top level, or the AppImage file itself.
func Install(pkg, target string) error {
	if strings.HasSuffix(target, ".app") {
		return installBundle(pkg, target)
	}
	tmp := target + ".new"
	in, err := os.Open(pkg)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// installBundle unpacks the new .app next to the old one and swaps them.
func installBundle(pkg, bundle string) error {
	staging := bundle + ".new"
	_ = os.RemoveAll(staging)
	if err := untar(pkg, staging); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	// The package holds Name.app/...; its single top folder is the new bundle.
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".app") {
		_ = os.RemoveAll(staging)
		return errors.New("the update package does not hold one app bundle")
	}
	newApp := filepath.Join(staging, entries[0].Name())
	old := bundle + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(newApp, bundle); err != nil {
		_ = os.Rename(old, bundle)
		return err
	}
	_ = os.RemoveAll(old)
	_ = os.RemoveAll(staging)
	return nil
}

func untar(pkg, dir string) error {
	f, err := os.Open(pkg)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("the update package has a path outside its folder")
		}
		p := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, 2<<30)); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "..") {
				return errors.New("the update package has a link outside its folder")
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		}
	}
}
