package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html"
)

// Limits of web_fetch.
const (
	MaxFetchBytes  = 10 << 20
	MaxOutputBytes = 64 << 10
)

// Fetch reads one page through the server client and returns its readable text: HTML as
// Markdown, PDF as text, and plain text as it is. It refuses private and local addresses
// and hosts that policy does not allow, also on each redirect. Errors name the host.
func Fetch(ctx context.Context, base *http.Client, policy Policy, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("web_fetch needs an absolute URL, such as https://example.com/page")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web_fetch reads http and https URLs only, not %s", u.Scheme)
	}
	u.Fragment = ""
	host := normHost(u.Hostname())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Fettle-web_fetch/1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,text/plain;q=0.9,*/*;q=0.5")
	resp, err := fetchClient(base, policy).Do(req)
	if err != nil {
		var refused *RefusedError
		if errors.As(err, &refused) {
			return "", refused
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("web_fetch could not read %s: %w", host, err)
	}
	defer resp.Body.Close()
	final := resp.Request.URL
	fhost := normHost(final.Hostname())
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("%s answered HTTP %d", fhost, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		return "", fmt.Errorf("web_fetch could not read %s: %w", fhost, err)
	}
	cut := len(body) > MaxFetchBytes
	if cut {
		body = body[:MaxFetchBytes]
	}
	ctype, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ctype == "" || ctype == "application/octet-stream" {
		ctype, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	if bytes.HasPrefix(body, []byte("%PDF-")) {
		ctype = "application/pdf"
	}
	var text string
	switch {
	case ctype == "text/html" || ctype == "application/xhtml+xml":
		text, err = htmlText(ctx, body, final)
	case ctype == "application/pdf":
		if cut {
			return "", fmt.Errorf("the PDF on %s is larger than 10 MB, so web_fetch cannot read it", fhost)
		}
		text, err = pdfText(body)
	case strings.HasPrefix(ctype, "text/") || ctype == "application/json" || ctype == "application/xml" ||
		strings.HasSuffix(ctype, "+json") || strings.HasSuffix(ctype, "+xml"):
		text = string(body)
	default:
		return "", fmt.Errorf("web_fetch cannot read %s content from %s. Use the browser for this file", ctype, fhost)
	}
	if err != nil {
		return "", fmt.Errorf("web_fetch could not read the page on %s: %w", fhost, err)
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	text = "Source: " + final.String() + "\n\n" + strings.TrimSpace(text)
	if cut {
		text += "\n\n[web_fetch read only the first 10 MB of the page.]"
	}
	return cutOutput(text), nil
}

// cutOutput keeps at most MaxOutputBytes, on a rune boundary, with a line that says so.
func cutOutput(s string) string {
	if len(s) <= MaxOutputBytes {
		return s
	}
	const note = "\n\n[The text was cut at 64 KB.]"
	n := MaxOutputBytes - len(note)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + note
}

// htmlText extracts the article with readability and converts it to Markdown. A page with
// no article converts as a whole.
func htmlText(ctx context.Context, body []byte, page *url.URL) (string, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	opts := []converter.ConvertOptionFunc{converter.WithContext(ctx), converter.WithDomain(page.Scheme + "://" + page.Host)}
	title := ""
	node := doc
	if art, err := readability.FromDocument(doc, page); err == nil && art.Node != nil {
		title, node = strings.TrimSpace(art.Title()), art.Node
	} else if doc, err = html.Parse(bytes.NewReader(body)); err == nil {
		node = doc
	}
	md, err := htmltomarkdown.ConvertNode(node, opts...)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(string(md))
	if title != "" && !strings.HasPrefix(out, "# ") {
		out = "# " + title + "\n\n" + out
	}
	return out, nil
}

// pdfText returns the text of each page of a PDF. The PDF reader panics on some broken
// files, so a panic becomes an error.
func pdfText(body []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("the PDF is broken: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("the PDF is broken: %w", err)
	}
	var b strings.Builder
	fonts := map[string]*pdf.Font{}
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		for _, name := range p.Fonts() {
			if _, ok := fonts[name]; !ok {
				f := p.Font(name)
				fonts[name] = &f
			}
		}
		t, err := p.GetPlainText(fonts)
		if err != nil {
			return "", fmt.Errorf("read page %d: %w", i, err)
		}
		b.WriteString(strings.TrimSpace(t))
		b.WriteString("\n\n")
		if b.Len() > MaxOutputBytes {
			break
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", errors.New("the PDF has no text layer. Use the browser to look at it")
	}
	return b.String(), nil
}
