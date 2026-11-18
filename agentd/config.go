package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Network modes.
const (
	NetworkOpen      = "open"
	NetworkAllowlist = "allowlist"
)

// NetworkPolicy is the outbound network policy of the computer.
type NetworkPolicy struct {
	Mode       string   `json:"mode"`
	AllowHosts []string `json:"allow_hosts"`
}

// Config is the agentd configuration. ConfigFromEnv builds it.
type Config struct {
	Token    string
	TLSCert  []byte
	TLSKey   []byte
	Insecure bool
	Addr     string
	// WSAddr is the plain listener of the WebSocket bridge (BridgeHandler); empty turns
	// the bridge off.
	WSAddr  string
	Home    string
	User    string
	Network NetworkPolicy
	Version string
	// CA holds extra root certificates as PEM. The boot step installs them.
	CA []byte
	// Proxy is the upstream proxy URL with the user name; ProxyPassword is its password.
	// With a proxy, all traffic of the computer goes through the filter proxy.
	Proxy         string
	ProxyPassword string
}

// SecretEnv lists the environment variables that hold secrets. The command
// unsets them after it reads the configuration so that no child sees them.
var SecretEnv = []string{"FETTLE_AGENTD_TOKEN", "FETTLE_AGENTD_TLS_CERT", "FETTLE_AGENTD_TLS_KEY", "FETTLE_AGENTD_PROXY_PASSWORD"}

// ConfigFromEnv reads the configuration from FETTLE_AGENTD_* variables.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Token:    getenv("FETTLE_AGENTD_TOKEN"),
		TLSCert:  []byte(getenv("FETTLE_AGENTD_TLS_CERT")),
		TLSKey:   []byte(getenv("FETTLE_AGENTD_TLS_KEY")),
		Insecure: getenv("FETTLE_AGENTD_INSECURE_LISTEN") == "1",
		Addr:     getenv("FETTLE_AGENTD_ADDR"),
		WSAddr:   getenv("FETTLE_AGENTD_WS_ADDR"),
		Home:     getenv("FETTLE_AGENTD_HOME"),
		User:     getenv("FETTLE_AGENTD_USER"),
		CA:       []byte(getenv("FETTLE_AGENTD_CA")),
		Proxy:    strings.TrimSpace(getenv("FETTLE_AGENTD_PROXY")),

		ProxyPassword: getenv("FETTLE_AGENTD_PROXY_PASSWORD"),
	}
	if c.Addr == "" {
		c.Addr = ":7421"
	}
	if c.Home == "" {
		c.Home = "/home/fettle"
	}
	if c.User == "" {
		c.User = "fettle"
	}
	if raw := strings.TrimSpace(getenv("FETTLE_AGENTD_NETWORK")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Network); err != nil {
			return Config{}, fmt.Errorf("FETTLE_AGENTD_NETWORK: %w", err)
		}
	}
	if c.Network.Mode == "" {
		c.Network.Mode = NetworkOpen
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if c.Token == "" {
		return errors.New("FETTLE_AGENTD_TOKEN is required")
	}
	if !c.Insecure && (len(c.TLSCert) == 0 || len(c.TLSKey) == 0) {
		return errors.New("FETTLE_AGENTD_TLS_CERT and FETTLE_AGENTD_TLS_KEY are required")
	}
	switch c.Network.Mode {
	case NetworkOpen, NetworkAllowlist:
	default:
		return fmt.Errorf("FETTLE_AGENTD_NETWORK: unknown mode %q", c.Network.Mode)
	}
	if c.Proxy != "" {
		u, err := url.Parse(c.Proxy)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("FETTLE_AGENTD_PROXY must be an http:// or https:// URL, got %q", c.Proxy)
		}
	}
	if !filepath.IsAbs(c.Home) {
		return fmt.Errorf("FETTLE_AGENTD_HOME must be an absolute path, got %q", c.Home)
	}
	c.Home = filepath.Clean(c.Home)
	return nil
}
