package vault

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "Fettle"
	keyringUser    = "master-key"
)

// MasterKeySource tells where the master key came from (for doctor).
type MasterKeySource string

const (
	SourceEnv      MasterKeySource = "environment"
	SourceKeychain MasterKeySource = "keychain"
	SourceFile     MasterKeySource = "file"
)

// LoadMasterKey returns the master key. Server mode needs FETTLE_MASTER_KEY. Local mode
// uses the OS keychain and falls back to a 0600 file in the data folder when no keychain exists.
func LoadMasterKey(envValue, dataDir string, server bool) ([]byte, MasterKeySource, error) {
	if envValue != "" {
		k, err := ParseMasterKey(envValue)
		return k, SourceEnv, err
	}
	if server {
		return nil, "", errors.New("server mode needs FETTLE_MASTER_KEY")
	}
	if os.Getenv("FETTLE_NO_KEYCHAIN") == "" {
		if v, err := keyring.Get(keyringService, keyringUser); err == nil {
			k, err := ParseMasterKey(v)
			return k, SourceKeychain, err
		} else if errors.Is(err, keyring.ErrNotFound) {
			k := NewKey()
			if err := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(k)); err == nil {
				return k, SourceKeychain, nil
			}
		}
	}
	path := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParseMasterKey(string(b))
		return k, SourceFile, err
	}
	k := NewKey()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k)), 0o600); err != nil {
		return nil, "", fmt.Errorf("write master key file: %w", err)
	}
	return k, SourceFile, nil
}
