package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// A key file from a start without a keychain stays the key of its data when the keychain
// works later; a keychain that does not answer never gets a data folder a new key.
func TestMasterKeyKeepsTheKeyOfExistingData(t *testing.T) {
	t.Setenv("FETTLE_NO_KEYCHAIN", "")
	dir := t.TempDir()

	keyring.MockInitWithError(errors.New("the keychain is locked"))
	first, src, err := LoadMasterKey("", dir, false)
	if err != nil || src != SourceFile {
		t.Fatalf("first start without a keychain: %v %s", err, src)
	}
	keyring.MockInit()
	again, src, err := LoadMasterKey("", dir, false)
	if err != nil || src != SourceFile || !bytes.Equal(first, again) {
		t.Fatalf("a working keychain replaced the key of the data: %v %s", err, src)
	}

	other := t.TempDir()
	_, src, err = LoadMasterKey("", other, false)
	if err != nil || src != SourceKeychain {
		t.Fatalf("a new folder with a working keychain: %v %s", err, src)
	}
	if err := os.WriteFile(filepath.Join(other, "fettle.db"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("the keychain is locked"))
	if _, _, err := LoadMasterKey("", other, false); err == nil {
		t.Fatal("a locked keychain gave a folder with data a new key")
	}
	if _, err := os.Stat(filepath.Join(other, "master.key")); err == nil {
		t.Fatal("a locked keychain wrote a new key file beside the data")
	}
	keyring.MockInit()
}
