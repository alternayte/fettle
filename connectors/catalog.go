// Package connectors holds the catalogue of known remote MCP servers (§12.1). The Connect
// screen shows every entry and also accepts any URL.
package connectors

import (
	_ "embed"
	"encoding/json"
)

//go:embed catalog.json
var raw []byte

// Entry is one known remote MCP server.
type Entry struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Auth       string `json:"auth" enum:"oauth,header,none"`
	HeaderName string `json:"header_name,omitempty"`
	Icon       string `json:"icon"`
}

// Catalog returns the catalogue entries.
func Catalog() ([]Entry, error) {
	var c struct {
		Connectors []Entry `json:"connectors"`
	}
	err := json.Unmarshal(raw, &c)
	return c.Connectors, err
}
