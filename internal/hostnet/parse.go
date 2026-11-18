package hostnet

import (
	"bufio"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// parseScutilProxy reads the output of `scutil --proxy`. It returns the HTTPS proxy when it
// is on, else the HTTP proxy, and the exceptions list as a NO_PROXY value.
func parseScutilProxy(out string) (proxy, noProxy string) {
	vals := map[string]string{}
	var exceptions []string
	inExceptions := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if inExceptions {
			if line == "}" {
				inExceptions = false
				continue
			}
			if _, v, ok := strings.Cut(line, " : "); ok {
				exceptions = append(exceptions, strings.TrimSpace(v))
			}
			continue
		}
		k, v, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "ExceptionsList" && strings.HasPrefix(v, "<array>") {
			inExceptions = true
			continue
		}
		vals[k] = v
	}
	port := func(k string) int {
		n, _ := strconv.Atoi(vals[k])
		return n
	}
	switch {
	case vals["HTTPSEnable"] == "1" && vals["HTTPSProxy"] != "":
		proxy = proxyURL(vals["HTTPSProxy"], port("HTTPSPort"))
	case vals["HTTPEnable"] == "1" && vals["HTTPProxy"] != "":
		proxy = proxyURL(vals["HTTPProxy"], port("HTTPPort"))
	}
	if proxy == "" {
		return "", ""
	}
	return proxy, noProxyList(exceptions)
}

// noProxyList turns host patterns (*.local, 169.254/16, <local>) into a NO_PROXY value.
func noProxyList(items []string) string {
	var out []string
	for _, it := range items {
		it = strings.TrimSpace(it)
		switch {
		case it == "", it == "<local>":
			continue
		case strings.HasPrefix(it, "*."):
			out = append(out, it[1:])
		default:
			out = append(out, it)
		}
	}
	return strings.Join(out, ",")
}

// deniedTrust reads the XML of `security trust-settings-export` and returns the SHA-1
// fingerprints (upper-case hex) of the certificates that have a deny result
// (kSecTrustSettingsResult 3) for any policy.
func deniedTrust(r io.Reader) (map[string]bool, error) {
	v, err := decodePlist(r)
	if err != nil {
		return nil, err
	}
	root, _ := v.(map[string]any)
	list, _ := root["trustList"].(map[string]any)
	out := map[string]bool{}
	for sha1, entry := range list {
		e, _ := entry.(map[string]any)
		settings, _ := e["trustSettings"].([]any)
		for _, s := range settings {
			m, _ := s.(map[string]any)
			if n, ok := m["kSecTrustSettingsResult"].(int64); ok && n == 3 {
				out[strings.ToUpper(sha1)] = true
			}
		}
	}
	return out, nil
}

// decodePlist decodes an XML property list into maps, slices, strings, int64 and bool.
// Data and date values decode as their text.
func decodePlist(r io.Reader) (any, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("read the property list: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local != "plist" {
			return plistValue(dec, se)
		}
	}
}

func plistValue(dec *xml.Decoder, se xml.StartElement) (any, error) {
	switch se.Name.Local {
	case "dict":
		out := map[string]any{}
		key := ""
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "key" {
					var k string
					if err := dec.DecodeElement(&k, &t); err != nil {
						return nil, err
					}
					key = k
					continue
				}
				v, err := plistValue(dec, t)
				if err != nil {
					return nil, err
				}
				out[key] = v
			case xml.EndElement:
				return out, nil
			}
		}
	case "array":
		var out []any
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				v, err := plistValue(dec, t)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			case xml.EndElement:
				return out, nil
			}
		}
	case "true", "false":
		if err := dec.Skip(); err != nil {
			return nil, err
		}
		return se.Name.Local == "true", nil
	default:
		var text string
		if err := dec.DecodeElement(&text, &se); err != nil {
			return nil, err
		}
		text = strings.TrimSpace(text)
		if se.Name.Local == "integer" {
			n, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				return nil, err
			}
			return n, nil
		}
		return text, nil
	}
}

// parseProxyServer reads a Windows proxy server value: "host:port", or a list such as
// "http=host:port;https=host:port". It prefers the https entry.
func parseProxyServer(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "=") {
		first, _, _ := strings.Cut(v, ";")
		return proxyAddr(first)
	}
	entries := map[string]string{}
	for _, part := range strings.Split(v, ";") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			entries[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(val)
		}
	}
	for _, k := range []string{"https", "http"} {
		if e := entries[k]; e != "" {
			return proxyAddr(e)
		}
	}
	return ""
}

func proxyAddr(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if strings.Contains(hostport, "://") {
		return hostport
	}
	return "http://" + hostport
}

// parseProxyOverride reads a Windows bypass list ("*.acme.test;<local>") as NO_PROXY.
func parseProxyOverride(v string) string {
	return noProxyList(strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ' ' }))
}

// parseWinHTTPSettings reads the WinHttpSettings registry blob that `netsh winhttp set
// proxy` writes: a header, flags, then the proxy server and the bypass list as strings with
// a length in front.
func parseWinHTTPSettings(b []byte) (proxy, noProxy string, err error) {
	if len(b) < 16 {
		return "", "", errors.New("the WinHTTP settings are too short")
	}
	flags := binary.LittleEndian.Uint32(b[8:12])
	n := int(binary.LittleEndian.Uint32(b[12:16]))
	if 16+n > len(b) {
		return "", "", errors.New("the WinHTTP proxy value is cut off")
	}
	server := string(b[16 : 16+n])
	rest := b[16+n:]
	var bypass string
	if len(rest) >= 4 {
		m := int(binary.LittleEndian.Uint32(rest[:4]))
		if 4+m <= len(rest) {
			bypass = string(rest[4 : 4+m])
		}
	}
	// Flag 2 is "use a named proxy"; without it, WinHTTP connects directly.
	if flags&2 == 0 {
		return "", "", nil
	}
	return parseProxyServer(server), parseProxyOverride(bypass), nil
}
