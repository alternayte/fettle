//go:build !drelgen

package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// KeySize is the size of the master key and of each data key (AES-256).
const KeySize = 32

// ErrDecrypt means a value does not decrypt with the given key.
var ErrDecrypt = errors.New("vault: value does not decrypt with this key")

// ParseMasterKey reads a base64 key or file:<path> (Appendix A).
func ParseMasterKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "file:") {
		b, err := os.ReadFile(strings.TrimPrefix(v, "file:"))
		if err != nil {
			return nil, fmt.Errorf("read master key file: %w", err)
		}
		v = strings.TrimSpace(string(b))
	}
	k, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("master key is not base64: %w", err)
	}
	if len(k) != KeySize {
		return nil, fmt.Errorf("master key has %d bytes, need %d", len(k), KeySize)
	}
	return k, nil
}

// NewKey returns a random 32-byte key.
func NewKey() []byte {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// seal encrypts plain with key and binds it to aad. It returns nonce and ciphertext.
func seal(key, plain, aad []byte) (nonce, ct []byte, err error) {
	a, err := gcm(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, a.Seal(nil, nonce, plain, aad), nil
}

func open(key, nonce, ct, aad []byte) ([]byte, error) {
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	p, err := a.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return p, nil
}

// wrapKey wraps a data key with the master key. The result is base64(nonce||ciphertext).
func wrapKey(master, dataKey []byte, aad string) (string, error) {
	n, ct, err := seal(master, dataKey, []byte(aad))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(n, ct...)), nil
}

func unwrapKey(master []byte, wrapped, aad string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(wrapped)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 {
		return nil, ErrDecrypt
	}
	return open(master, b[:12], b[12:], []byte(aad))
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
