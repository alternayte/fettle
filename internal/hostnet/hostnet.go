// Package hostnet reads the trust and proxy settings of the host that runs the desktop app:
// the root certificates that an admin or MDM added, and the system proxy. On Linux and other
// systems it reads nothing.
package hostnet

import (
	"net"
	"strconv"
	"strings"
)

// Settings are the host values that computers and the server use.
type Settings struct {
	// RootsPEM holds the extra root certificates of the host as PEM.
	RootsPEM []byte
	// Proxy is the system proxy as a URL, or "".
	Proxy string
	// NoProxy is the bypass list of the system proxy, comma separated.
	NoProxy string
}

// proxyURL returns http://host:port, or "" when host is empty.
func proxyURL(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return host
	}
	if port <= 0 {
		port = 80
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}
