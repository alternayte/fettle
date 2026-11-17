//go:build windows

package hostnet

import (
	"context"
	"crypto/x509"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// rootStores are the ROOT stores in the registry that an admin, Group Policy or MDM fills.
var rootStores = []uint32{
	windows.CERT_SYSTEM_STORE_LOCAL_MACHINE,
	windows.CERT_SYSTEM_STORE_LOCAL_MACHINE_GROUP_POLICY,
	windows.CERT_SYSTEM_STORE_LOCAL_MACHINE_ENTERPRISE,
	windows.CERT_SYSTEM_STORE_CURRENT_USER,
	windows.CERT_SYSTEM_STORE_CURRENT_USER_GROUP_POLICY,
}

// Read returns the self-signed CA certificates in the ROOT registry stores, and the proxy of
// the current user (Internet Options), else the WinHTTP proxy.
func Read(_ context.Context) (Settings, error) {
	var s Settings
	var certs []*x509.Certificate
	for _, loc := range rootStores {
		certs = append(certs, storeCerts(loc)...)
	}
	var keep []*x509.Certificate
	for _, c := range certs {
		if selfSignedCA(c) {
			keep = append(keep, c)
		}
	}
	s.RootsPEM = encodePEM(keep)
	s.Proxy, s.NoProxy = userProxy()
	if s.Proxy == "" {
		s.Proxy, s.NoProxy = winHTTPProxy()
	}
	return s, nil
}

// storeCerts reads one ROOT registry store. A store that does not exist gives nothing.
func storeCerts(location uint32) []*x509.Certificate {
	name, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return nil
	}
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM_REGISTRY_W, 0, 0,
		location|windows.CERT_STORE_READONLY_FLAG|windows.CERT_STORE_OPEN_EXISTING_FLAG, uintptr(unsafe.Pointer(name)))
	if err != nil {
		return nil
	}
	defer func() { _ = windows.CertCloseStore(store, 0) }()
	var out []*x509.Certificate
	var ctx *windows.CertContext
	for {
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if err != nil || ctx == nil {
			return out
		}
		raw := unsafe.Slice(ctx.EncodedCert, ctx.Length)
		if c, err := x509.ParseCertificate(append([]byte(nil), raw...)); err == nil {
			out = append(out, c)
		}
	}
}

func userProxy() (proxy, noProxy string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer func() { _ = k.Close() }()
	on, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || on == 0 {
		return "", ""
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil {
		return "", ""
	}
	// A missing bypass list reads as empty.
	override, _, _ := k.GetStringValue("ProxyOverride")
	return parseProxyServer(server), parseProxyOverride(override)
}

func winHTTPProxy() (proxy, noProxy string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\Connections`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer func() { _ = k.Close() }()
	b, _, err := k.GetBinaryValue("WinHttpSettings")
	if err != nil {
		return "", ""
	}
	proxy, noProxy, err = parseWinHTTPSettings(b)
	if err != nil {
		return "", ""
	}
	return proxy, noProxy
}
