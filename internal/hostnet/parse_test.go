package hostnet

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestScutilProxy(t *testing.T) {
	out := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
    2 : *.acme.test
  }
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : web.proxy.acme.test
  HTTPSEnable : 1
  HTTPSPort : 3128
  HTTPSProxy : proxy.acme.test
  ProxyAutoConfigEnable : 0
}`
	p, np := parseScutilProxy(out)
	if p != "http://proxy.acme.test:3128" || np != ".local,169.254/16,.acme.test" {
		t.Fatalf("proxy %q, no proxy %q", p, np)
	}
	p, _ = parseScutilProxy(strings.Replace(out, "HTTPSEnable : 1", "HTTPSEnable : 0", 1))
	if p != "http://web.proxy.acme.test:8080" {
		t.Fatalf("HTTP proxy %q", p)
	}
	if p, np := parseScutilProxy("<dictionary> {\n  HTTPEnable : 0\n}"); p != "" || np != "" {
		t.Fatalf("no proxy: %q %q", p, np)
	}
}

func TestTrustSettingsDeny(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>trustList</key>
	<dict>
		<key>0563b8630d62d75abbc8ab1e4bdfb5a899b24d43</key>
		<dict>
			<key>issuerName</key>
			<data>MBQxEjAQBgNVBAMMCUFjbWUgUm9vdA==</data>
			<key>modDate</key>
			<date>2026-01-01T00:00:00Z</date>
			<key>trustSettings</key>
			<array>
				<dict>
					<key>kSecTrustSettingsPolicyName</key>
					<string>sslServer</string>
					<key>kSecTrustSettingsResult</key>
					<integer>3</integer>
				</dict>
			</array>
		</dict>
		<key>1111111111111111111111111111111111111111</key>
		<dict>
			<key>trustSettings</key>
			<array/>
		</dict>
		<key>2222222222222222222222222222222222222222</key>
		<dict>
			<key>trustSettings</key>
			<array>
				<dict>
					<key>kSecTrustSettingsResult</key>
					<integer>1</integer>
				</dict>
			</array>
		</dict>
	</dict>
	<key>trustVersion</key>
	<integer>1</integer>
</dict>
</plist>`
	d, err := deniedTrust(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 || !d["0563B8630D62D75ABBC8AB1E4BDFB5A899B24D43"] {
		t.Fatalf("denied %v", d)
	}
}

func TestWindowsProxyValues(t *testing.T) {
	for in, want := range map[string]string{
		"proxy.acme.test:8080":                              "http://proxy.acme.test:8080",
		"http=web.acme.test:80;https=secure.acme.test:3128": "http://secure.acme.test:3128",
		"ftp=ftp.acme.test:21;http=web.acme.test:80":        "http://web.acme.test:80",
		"": "",
	} {
		if got := parseProxyServer(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := parseProxyOverride("*.acme.test;<local>;10.*"); got != ".acme.test,10.*" {
		t.Errorf("override %q", got)
	}
	blob := func(flags uint32, server, bypass string) []byte {
		b := make([]byte, 16)
		binary.LittleEndian.PutUint32(b[0:], 0x28)
		binary.LittleEndian.PutUint32(b[8:], flags)
		binary.LittleEndian.PutUint32(b[12:], uint32(len(server)))
		b = append(b, server...)
		n := make([]byte, 4)
		binary.LittleEndian.PutUint32(n, uint32(len(bypass)))
		return append(append(b, n...), bypass...)
	}
	p, np, err := parseWinHTTPSettings(blob(3, "proxy.acme.test:3128", "*.acme.test;<local>"))
	if err != nil || p != "http://proxy.acme.test:3128" || np != ".acme.test" {
		t.Fatalf("WinHTTP: %q %q %v", p, np, err)
	}
	if p, _, err := parseWinHTTPSettings(blob(1, "", "")); err != nil || p != "" {
		t.Fatalf("direct WinHTTP: %q %v", p, err)
	}
	if _, _, err := parseWinHTTPSettings([]byte{1, 2}); err == nil {
		t.Fatal("a short blob parses")
	}
}
