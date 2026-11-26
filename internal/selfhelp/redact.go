package selfhelp

import (
	"regexp"
	"sort"
	"strings"
)

// PublicHosts are host names that an issue can keep: GitHub, the model, search and
// computer providers that Fettle knows, and the example domains. A subdomain of one of
// them is public too.
var PublicHosts = []string{
	"github.com", "githubusercontent.com", "githubcopilot.com",
	"openrouter.ai", "openai.com", "anthropic.com", "googleapis.com", "deepseek.com",
	"ollama.com", "lmstudio.ai",
	"tavily.com", "exa.ai", "search.brave.com", "searxng.org",
	"e2b.dev", "docker.com", "docker.io", "ghcr.io",
	"example.com", "example.org", "example.net",
	"fettle.dev",
}

// hostTLDs are the top-level names that make a dotted word a host name. File extensions
// that are also country codes (md, py, sh, rs, ts, so) are not in the list, so file names
// stay.
var hostTLDs = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`com net org io dev ai co app cloud biz info me tech site online xyz
		internal local lan corp intranet intra home localdomain private priv test example invalid
		uk de fr nl eu us ca au jp in ch se no dk fi es it pl be at ie nz za br cn kr sg hk tw ru`) {
		hostTLDs[t] = true
	}
}

var (
	reCredURL = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@:]+(?::[^\s/@]*)?@`)
	reEmail   = regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`)
	// Home folders on Linux, macOS and Windows. The user part goes.
	reHome = regexp.MustCompile(`(?i)(/home/|/Users/|[A-Z]:\\Users\\|/root\b)[^\s/\\"'<>)]*`)
	reIPv4 = regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}\b`)
	// An address with "::", or with 4 to 8 groups. A time such as 12:30:45 has 3 groups.
	reIPv6    = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){1,7}:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*)?|\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b`)
	reToken   = regexp.MustCompile(`\b(?:sk-|ghp_|gho_|ghs_|github_pat_|xox[abp]-|AKIA)[A-Za-z0-9_\-]{8,}|\b[A-Za-z0-9_\-+/=]{32,}`)
	reHost    = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+([a-z]{2,24})\b`)
	reVersion = regexp.MustCompile(`^v?\d+(\.\d+)+`)
)

// Redact removes personal and private details from issue text: URL credentials, email
// addresses, home folder paths, IP addresses, long tokens, host names that are not in
// PublicHosts, and each of names (user names and other words to hide). The repository is
// public, so a draft must not carry details of the person or of their network.
func Redact(s string, names ...string) string {
	s = reCredURL.ReplaceAllString(s, "${1}[credentials]@")
	s = reEmail.ReplaceAllString(s, "[email]")
	s = reHome.ReplaceAllString(s, "~")
	s = reToken.ReplaceAllStringFunc(s, func(m string) string {
		if !mixed(m) {
			return m
		}
		return "[token]"
	})
	s = reIPv4.ReplaceAllString(s, "[ip]")
	s = reIPv6.ReplaceAllString(s, "[ip]")
	s = reHost.ReplaceAllStringFunc(s, func(m string) string {
		if reVersion.MatchString(m) {
			return m
		}
		parts := strings.Split(strings.ToLower(m), ".")
		if !hostTLDs[parts[len(parts)-1]] || publicHost(strings.ToLower(m)) {
			return m
		}
		return "[host]"
	})
	// Longest names first, so that a name inside another name does not leave a part.
	ns := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); len([]rune(n)) >= 3 {
			ns = append(ns, n)
		}
	}
	sort.Slice(ns, func(i, j int) bool { return len(ns[i]) > len(ns[j]) })
	for _, n := range ns {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(n) + `\b`)
		s = re.ReplaceAllString(s, "[user]")
	}
	return s
}

// mixed reports whether a long word has letters and digits, as a key or a token does. A
// long word of letters only (a long identifier) stays.
func mixed(s string) bool {
	if strings.HasPrefix(s, "sk-") || strings.HasPrefix(s, "gh") || strings.HasPrefix(s, "xox") || strings.HasPrefix(s, "AKIA") {
		return true
	}
	var l, d bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			d = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			l = true
		}
	}
	return l && d
}

func publicHost(h string) bool {
	if h == "localhost" {
		return true
	}
	for _, p := range PublicHosts {
		if h == p || strings.HasSuffix(h, "."+p) {
			return true
		}
	}
	return false
}
