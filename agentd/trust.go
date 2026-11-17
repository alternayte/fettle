package agentd

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// caBundle is the system CA bundle that update-ca-certificates writes.
const caBundle = "/etc/ssl/certs/ca-certificates.crt"

// parseRoots returns the certificates of a PEM bundle. A block that is not a certificate
// is an error, so a wrong value fails at boot and not at the first request.
func parseRoots(data []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	for {
		var blk *pem.Block
		blk, data = pem.Decode(data)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("FETTLE_AGENTD_CA holds a %s block, not a certificate", blk.Type)
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, fmt.Errorf("FETTLE_AGENTD_CA: %w", err)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("FETTLE_AGENTD_CA holds no certificate")
	}
	return out, nil
}

// installRoots adds the extra roots to every trust store of the computer under root: the
// system store (update-ca-certificates through run), and the managed Chromium policy,
// because Chromium on Linux does not read the system store.
func installRoots(root string, data []byte, run func(name string, args ...string) error) error {
	certs, err := parseRoots(data)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "usr", "local", "share", "ca-certificates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	der := make([]string, 0, len(certs))
	for i, c := range certs {
		b := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("fettle-%02d.crt", i+1)), b, 0o644); err != nil {
			return err
		}
		der = append(der, base64.StdEncoding.EncodeToString(c.Raw))
	}
	if run != nil {
		if err := run("update-ca-certificates"); err != nil {
			return err
		}
	}
	policies := filepath.Join(root, "etc", "chromium", "policies", "managed")
	if err := os.MkdirAll(policies, 0o755); err != nil {
		return err
	}
	policy, err := json.Marshal(map[string]any{"CACertificates": der})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(policies, "fettle.json"), policy, 0o644)
}

// rootsEnv is the environment that makes tools with their own CA list use the system
// bundle, which holds the extra roots.
func rootsEnv() map[string]string {
	return map[string]string{
		"SSL_CERT_FILE":       caBundle,
		"SSL_CERT_DIR":        filepath.Dir(caBundle),
		"NODE_EXTRA_CA_CERTS": caBundle,
		"REQUESTS_CA_BUNDLE":  caBundle,
		"UV_NATIVE_TLS":       "1",
	}
}
