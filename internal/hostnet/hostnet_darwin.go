//go:build darwin

package hostnet

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	systemKeychain = "/Library/Keychains/System.keychain"
	appleRoots     = "/System/Library/Keychains/SystemRootCertificates.keychain"
)

// Read returns the self-signed CA certificates in the System keychain that are not Apple
// roots and have no deny trust setting, and the system proxy from `scutil --proxy`.
func Read(ctx context.Context) (Settings, error) {
	var s Settings
	roots, err := keychainRoots(ctx)
	if err != nil {
		return s, err
	}
	s.RootsPEM = roots
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return s, fmt.Errorf("scutil --proxy: %w", err)
	}
	s.Proxy, s.NoProxy = parseScutilProxy(string(out))
	return s, nil
}

func keychainRoots(ctx context.Context) ([]byte, error) {
	all, err := exec.CommandContext(ctx, "security", "find-certificate", "-a", "-p", systemKeychain).Output()
	if err != nil {
		return nil, fmt.Errorf("read the System keychain: %w", err)
	}
	apple := map[[32]byte]bool{}
	if out, err := exec.CommandContext(ctx, "security", "find-certificate", "-a", "-p", appleRoots).Output(); err == nil {
		for _, c := range parsePEM(out) {
			apple[sha256.Sum256(c.Raw)] = true
		}
	}
	denied, err := adminDenies(ctx)
	if err != nil {
		return nil, err
	}
	var keep []*x509.Certificate
	for _, c := range parsePEM(all) {
		if !selfSignedCA(c) || apple[sha256.Sum256(c.Raw)] {
			continue
		}
		sum := sha1.Sum(c.Raw)
		if denied[strings.ToUpper(hex.EncodeToString(sum[:]))] {
			continue
		}
		keep = append(keep, c)
	}
	return encodePEM(keep), nil
}

// adminDenies exports the admin trust settings and returns the certificates they deny.
func adminDenies(ctx context.Context) (map[string]bool, error) {
	dir, err := os.MkdirTemp("", "fettle-trust-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "trust.plist")
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "security", "trust-settings-export", "-d", file)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// With no admin trust settings, the export fails and nothing is denied.
		if strings.Contains(stderr.String(), "No Trust Settings") {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("security trust-settings-export: %s", strings.TrimSpace(stderr.String()))
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return deniedTrust(f)
}

// parsePEM returns the certificates in a PEM bundle; blocks that do not parse are left out.
func parsePEM(b []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return out
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
			out = append(out, c)
		}
	}
}
