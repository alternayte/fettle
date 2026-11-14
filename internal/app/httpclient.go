package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// NewHTTPClient returns the client for all outbound HTTP (C-08, REQ-CORE-007). It uses
// HTTP_PROXY, HTTPS_PROXY and NO_PROXY, and trusts the system CAs plus extraCAFile.
func NewHTTPClient(extraCAFile string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if extraCAFile != "" {
		pem, err := os.ReadFile(extraCAFile)
		if err != nil {
			return nil, fmt.Errorf("read FETTLE_EXTRA_CA_FILE: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("FETTLE_EXTRA_CA_FILE has no PEM certificates")
		}
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	return &http.Client{Transport: tr}, nil
}
