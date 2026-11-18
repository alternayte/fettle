//go:build darwin || windows

package hostnet

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
)

// selfSignedCA reports whether c is a CA certificate that signs itself, which is the shape of
// a root that an admin adds for TLS inspection.
func selfSignedCA(c *x509.Certificate) bool {
	if !c.IsCA || !bytes.Equal(c.RawSubject, c.RawIssuer) {
		return false
	}
	return c.CheckSignatureFrom(c) == nil
}

// encodePEM writes certificates as one PEM bundle, each certificate once.
func encodePEM(certs []*x509.Certificate) []byte {
	var b bytes.Buffer
	seen := map[[32]byte]bool{}
	for _, c := range certs {
		sum := sha256.Sum256(c.Raw)
		if seen[sum] {
			continue
		}
		seen[sum] = true
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return b.Bytes()
}
