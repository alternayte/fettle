// Package desktop holds the parts of the desktop app (§15.1) that need no window: its
// settings file and signed updates. cmd/fettle-desktop is the Wails shell around them.
package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Modes of the desktop app (§15.3, step 1).
const (
	ModeUnset  = ""       // first run: the user has not chosen
	ModeLocal  = "local"  // this computer: the app runs the server in process
	ModeRemote = "remote" // connect to a server: the window loads its URL
)

// Config is the desktop app's own settings, outside the server's database.
type Config struct {
	Mode      string `json:"mode"`
	ServerURL string `json:"server_url,omitempty"`
	// UpdateFeed overrides the release feed (tests and private builds).
	UpdateFeed string `json:"update_feed,omitempty"`
}

// Path is the settings file in the app-data folder.
func Path(dataDir string) string { return filepath.Join(dataDir, "desktop.json") }

// Load reads the settings; a missing file is a first run.
func Load(dataDir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(Path(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// Save writes the settings with mode 0600.
func Save(dataDir string, c Config) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := Path(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dataDir))
}
