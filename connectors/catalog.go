// Package connectors holds the catalogue of known MCP servers (§12.1). The Connect screen
// shows every entry and also accepts any URL or command.
package connectors

import (
	_ "embed"
	"encoding/json"
)

//go:embed catalog.json
var raw []byte

// Variable is one environment variable that a stdio MCP server reads. A secret variable
// belongs to an account and is sealed; a plain one belongs to the connector.
type Variable struct {
	Name     string `json:"name" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" maxLength:"100"`
	Required bool   `json:"required"`
	Secret   bool   `json:"secret"`
	Doc      string `json:"doc,omitempty" maxLength:"300"`
}

// Entry is one known MCP server: a remote URL, or a stdio command that runs in the computer.
type Entry struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind" enum:"mcp_remote,mcp_stdio"`
	URL        string     `json:"url,omitempty"`
	Command    []string   `json:"command,omitempty"`
	Auth       string     `json:"auth" enum:"oauth,header,none"`
	HeaderName string     `json:"header_name,omitempty"`
	Icon       string     `json:"icon"`
	Variables  []Variable `json:"variables,omitempty"`
}

// Catalog returns the catalogue entries.
func Catalog() ([]Entry, error) {
	var c struct {
		Connectors []Entry `json:"connectors"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	for i := range c.Connectors {
		if c.Connectors[i].Kind == "" {
			c.Connectors[i].Kind = "mcp_remote"
		}
	}
	return c.Connectors, nil
}
