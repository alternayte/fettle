package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/vault"
)

// A first start on a host with no keychain, such as Linux without D-Bus, writes a key file.
// New must not create fettle.db before the key loads, or the empty database reads as data
// sealed with a keychain key that does not answer.
func TestFirstStartWithoutKeychainWritesAKeyFile(t *testing.T) {
	t.Setenv("FETTLE_NO_KEYCHAIN", "")
	keyring.MockInitWithError(errors.New("dbus-launch not found"))
	t.Cleanup(keyring.MockInit)
	cfg := testenv.Config(t, "sqlite")
	cfg.MasterKey = ""
	a, err := app.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.DB.Close)
	if a.MasterKeySource != vault.SourceFile {
		t.Fatalf("source %s", a.MasterKeySource)
	}
}
