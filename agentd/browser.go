package agentd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/alternayte/fettle/agentd/client"
)

// browserCtx is one browser context: a Chromium process with its own profile and one tab.
type browserCtx struct {
	id        string
	profile   string // absolute profile folder
	downloads string // absolute downloads folder
	ephemeral bool

	mu          sync.Mutex // one operation at a time
	tab         context.Context
	cancelAlloc context.CancelFunc
	cancelTab   context.CancelFunc

	dmu     sync.Mutex
	dialogs []string

	// seen holds the elements of the last snapshot that agentd returned to the bot. An
	// action refuses when the element differs from what the bot saw (§10.7).
	seen map[string]client.Element
}

func (c *browserCtx) remember(s client.Snapshot) {
	c.seen = make(map[string]client.Element, len(s.Elements))
	for _, e := range s.Elements {
		c.seen[e.Ref] = e
	}
}

type browsers struct {
	mu  sync.Mutex
	m   map[string]*browserCtx
	bin string
}

func newBrowsers() *browsers {
	return &browsers{m: map[string]*browserCtx{}, bin: os.Getenv("CHROME_BIN")}
}

func (b *browsers) closeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, c := range b.m {
		c.close()
		delete(b.m, id)
	}
}

func (c *browserCtx) close() {
	c.cancelTab()
	c.cancelAlloc()
	if c.ephemeral {
		_ = os.RemoveAll(c.profile)
	}
}

// homePath resolves a slash path relative to the home folder for the browser process. The
// home folder is the root of every file operation, so the path cannot leave it.
func (s *Server) homePath(rel string) (string, error) {
	clean := path.Clean("/" + strings.TrimSpace(rel))
	if clean == "/" {
		return "", badRequest("path is empty")
	}
	return filepath.Join(s.home, filepath.FromSlash(clean[1:])), nil
}

func (s *Server) openBrowser(req client.BrowserContextRequest) (*browserCtx, error) {
	s.browsers.mu.Lock()
	defer s.browsers.mu.Unlock()
	if c, ok := s.browsers.m[req.ID]; ok {
		return c, nil
	}
	c := &browserCtx{id: req.ID, ephemeral: req.Ephemeral}
	var err error
	if req.Ephemeral {
		if c.profile, err = os.MkdirTemp("", "fettle-browser-"); err != nil {
			return nil, err
		}
		c.downloads = filepath.Join(c.profile, "Downloads")
	} else {
		if c.profile, err = s.homePath(req.Profile); err != nil {
			return nil, err
		}
		if c.downloads, err = s.homePath(req.Downloads); err != nil {
			return nil, err
		}
	}
	for _, d := range []string{c.profile, c.downloads} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.UserDataDir(c.profile),
		chromedp.WindowSize(1280, 800),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("password-store", "basic"),
	)
	if s.browsers.bin != "" {
		opts = append(opts, chromedp.ExecPath(s.browsers.bin))
	}
	// Chromium's own sandbox needs privileges that a container does not give a non-root
	// user. The computer is the sandbox; the image sets this variable.
	if os.Getenv("FETTLE_BROWSER_NO_SANDBOX") == "1" {
		opts = append(opts, chromedp.NoSandbox)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	tab, cancelTab := chromedp.NewContext(allocCtx)
	c.tab, c.cancelAlloc, c.cancelTab = tab, cancelAlloc, cancelTab
	chromedp.ListenTarget(tab, func(ev any) {
		if d, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			c.dmu.Lock()
			c.dialogs = append(c.dialogs, fmt.Sprintf("%s: %s", d.Type, d.Message))
			c.dmu.Unlock()
			// Dialogs are never accepted automatically (§10.7).
			go func() { _ = chromedp.Run(tab, page.HandleJavaScriptDialog(false)) }()
		}
	})
	// The first Run starts the browser and binds it to the context it gets, so it gets the
	// tab context itself; a timer bounds the start instead of a deadline.
	started := make(chan error, 1)
	go func() {
		started <- chromedp.Run(tab, browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(c.downloads).WithEventsEnabled(false))
	}()
	select {
	case err = <-started:
	case <-time.After(60 * time.Second):
		err = errors.New("it did not start in 60 seconds")
	}
	if err != nil {
		c.close()
		return nil, &httpError{status: http.StatusServiceUnavailable, msg: "the browser did not start: " + err.Error()}
	}
	s.browsers.m[req.ID] = c
	return c, nil
}

func (s *Server) browserOf(r *http.Request) (*browserCtx, error) {
	id := r.PathValue("ctx")
	s.browsers.mu.Lock()
	defer s.browsers.mu.Unlock()
	c, ok := s.browsers.m[id]
	if !ok {
		return nil, &httpError{status: http.StatusNotFound, msg: "no browser context " + id}
	}
	return c, nil
}

func (c *browserCtx) takeDialogs() []string {
	c.dmu.Lock()
	defer c.dmu.Unlock()
	d := c.dialogs
	c.dialogs = nil
	return d
}

func (s *Server) createBrowserContext(w http.ResponseWriter, r *http.Request) {
	var req client.BrowserContextRequest
	if err := decodeJSON(w, r, 64<<10, &req); err != nil {
		s.fail(w, "browser", err)
		return
	}
	if req.ID == "" || strings.ContainsAny(req.ID, "/\\") {
		s.fail(w, "browser", badRequest("context id is missing or not valid"))
		return
	}
	if !req.Ephemeral && (req.Profile == "" || req.Downloads == "") {
		s.fail(w, "browser", badRequest("a persistent context needs a profile and a downloads folder"))
		return
	}
	if _, err := s.openBrowser(req); err != nil {
		s.fail(w, "browser", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": req.ID})
}

func (s *Server) deleteBrowserContext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.browsers.mu.Lock()
	c, ok := s.browsers.m[id]
	delete(s.browsers.m, id)
	s.browsers.mu.Unlock()
	if !ok {
		s.fail(w, "browser", &httpError{status: http.StatusNotFound, msg: "no browser context " + id})
		return
	}
	c.close()
	w.WriteHeader(http.StatusNoContent)
}

// snapshotJS lists the page's interactive elements and gives each a stable ref. Password and
// card fields are masked (§10.7).
const snapshotJS = `(() => {
  const sel = 'a[href],button,input,select,textarea,summary,[role=button],[role=link],[role=checkbox],[role=radio],[role=menuitem],[role=tab],[role=option],[role=switch],[contenteditable=""],[contenteditable=true],[onclick]';
  let n = window.__fettleNext || 0;
  const clip = (s, m) => { s = (s || '').replace(/\s+/g, ' ').trim(); return s.length > m ? s.slice(0, m) : s; };
  const roleOf = (el) => {
    const r = el.getAttribute('role'); if (r) return r;
    const t = el.tagName.toLowerCase();
    if (t === 'a') return 'link';
    if (t === 'button' || t === 'summary') return 'button';
    if (t === 'select') return 'combobox';
    if (t === 'textarea') return 'textbox';
    if (t === 'input') {
      const ty = (el.getAttribute('type') || 'text').toLowerCase();
      if (['submit','button','reset','image'].includes(ty)) return 'button';
      if (ty === 'checkbox') return 'checkbox';
      if (ty === 'radio') return 'radio';
      return 'textbox';
    }
    if (el.isContentEditable) return 'textbox';
    return 'generic';
  };
  const nameOf = (el) => {
    let s = el.getAttribute('aria-label');
    if (s) return clip(s, 100);
    const lb = el.getAttribute('aria-labelledby');
    if (lb) { s = lb.split(/\s+/).map(id => { const x = document.getElementById(id); return x ? x.innerText : ''; }).join(' '); if (clip(s, 100)) return clip(s, 100); }
    if (el.id) { const l = document.querySelector('label[for="' + CSS.escape(el.id) + '"]'); if (l && clip(l.innerText, 100)) return clip(l.innerText, 100); }
    const pl = el.closest('label'); if (pl && clip(pl.innerText, 100)) return clip(pl.innerText, 100);
    const t = el.tagName.toLowerCase();
    if (t === 'input' && ['submit','button','reset'].includes((el.type || '').toLowerCase())) return clip(el.value, 100);
    if (t !== 'input' && t !== 'select' && t !== 'textarea') { s = clip(el.innerText, 100); if (s) return s; const img = el.querySelector('img[alt]'); if (img) return clip(img.alt, 100); }
    return clip(el.getAttribute('placeholder') || el.getAttribute('title') || el.getAttribute('name') || '', 100);
  };
  const isSecret = (el) => {
    if (el.tagName.toLowerCase() !== 'input') return false;
    if ((el.type || '').toLowerCase() === 'password') return true;
    const ac = (el.getAttribute('autocomplete') || '').toLowerCase();
    if (ac.startsWith('cc-')) return true;
    return /(card.?num|ccnum|cc-?num|cvc|cvv|security.?code)/i.test((el.name || '') + ' ' + (el.id || ''));
  };
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    if (out.length >= 80) break;
    if (el.closest('[aria-hidden=true]')) continue;
    if ((el.getAttribute('type') || '').toLowerCase() === 'hidden') continue;
    const r = el.getBoundingClientRect(); const cs = getComputedStyle(el);
    if ((r.width === 0 && r.height === 0) || cs.visibility === 'hidden' || cs.display === 'none') continue;
    let ref = el.getAttribute('data-fettle-ref');
    if (!ref) { ref = 'e' + (++n); el.setAttribute('data-fettle-ref', ref); }
    const t = el.tagName.toLowerCase();
    const e = { ref, role: roleOf(el), name: nameOf(el) };
    if (t === 'input' || t === 'textarea' || t === 'select') {
      if (isSecret(el)) { e.masked = true; e.value = el.value ? '••••' : ''; }
      else if (el.type === 'checkbox' || el.type === 'radio') { e.checked = el.checked; }
      else { e.value = clip(el.value, 100); }
    }
    if (t === 'a') e.href = clip(el.href, 300);
    const f = el.form || el.closest('form');
    if (f) {
      e.form = clip(f.getAttribute('aria-label') || f.getAttribute('name') || f.id || (f.getAttribute('action') || ''), 100);
      const ty = (el.getAttribute('type') || '').toLowerCase();
      e.submits = (t === 'button' && (ty === '' || ty === 'submit')) || (t === 'input' && (ty === 'submit' || ty === 'image'));
    }
    if (el.disabled) e.disabled = true;
    out.push(e);
  }
  window.__fettleNext = n;
  const text = document.body ? document.body.innerText : '';
  return { url: location.href, title: document.title, text: text.length > 8000 ? text.slice(0, 8000) : text, truncated: text.length > 8000, elements: out };
})()`

func (c *browserCtx) snapshot(ctx context.Context) (client.Snapshot, error) {
	var snap client.Snapshot
	err := chromedp.Run(ctx, chromedp.Evaluate(snapshotJS, &snap))
	return snap, err
}

func (s *Server) browserNavigate(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	var req client.NavigateRequest
	if err := decodeJSON(w, r, 64<<10, &req); err != nil {
		s.fail(w, "browser", err)
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		s.fail(w, "browser", badRequest("the URL must start with http:// or https://"))
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate(u.String())); err != nil {
		s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the page did not load: " + cleanCDP(err)})
		return
	}
	snap, err := c.snapshot(ctx)
	if err != nil {
		s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the page could not be read: " + cleanCDP(err)})
		return
	}
	snap.Dialogs = c.takeDialogs()
	c.remember(snap)
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) browserSnapshot(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 15*time.Second)
	defer cancel()
	snap, err := c.snapshot(ctx)
	if err != nil {
		s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the page could not be read: " + cleanCDP(err)})
		return
	}
	snap.Dialogs = c.takeDialogs()
	// A peek is the server's own check before an action; it does not change what the bot saw.
	if r.URL.Query().Get("peek") != "1" {
		c.remember(snap)
	}
	writeJSON(w, http.StatusOK, snap)
}

func cleanCDP(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "it took too long"
	}
	return err.Error()
}

func findElement(snap client.Snapshot, ref string) *client.Element {
	for i := range snap.Elements {
		if snap.Elements[i].Ref == ref {
			return &snap.Elements[i]
		}
	}
	return nil
}

var actions = map[string]bool{"click": true, "type": true, "select": true, "check": true, "scroll": true, "hover": true, "press": true}

// browserAct runs one action on a ref. It revalidates the ref against a fresh snapshot right
// before it acts, and refuses when the element changed (§10.7). It never repeats an action.
func (s *Server) browserAct(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	var req client.ActRequest
	if err := decodeJSON(w, r, 64<<10, &req); err != nil {
		s.fail(w, "browser", err)
		return
	}
	if !actions[req.Action] {
		s.fail(w, "browser", badRequest("action must be click, type, select, check, scroll, hover or press"))
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 30*time.Second)
	defer cancel()
	res := client.ActResult{}
	refuse := func(msg string) {
		res.Refused = msg
		writeJSON(w, http.StatusOK, res)
	}
	var el *client.Element
	if req.Ref != "" || req.Action != "scroll" && req.Action != "press" {
		fresh, err := c.snapshot(ctx)
		if err != nil {
			s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the page could not be read: " + cleanCDP(err)})
			return
		}
		el = findElement(fresh, req.Ref)
		if el == nil {
			refuse("The element " + req.Ref + " is not on the page now. Take a new snapshot.")
			return
		}
		if prev, ok := c.seen[req.Ref]; !ok {
			refuse("You have not seen the element " + req.Ref + " in a snapshot of this page. Take a new snapshot.")
			return
		} else if prev.Role != el.Role || prev.Name != el.Name {
			refuse(fmt.Sprintf("The element %s changed since your last snapshot: it was %s %q and is now %s %q. Take a new snapshot.", req.Ref, prev.Role, prev.Name, el.Role, el.Name))
			return
		}
		if (req.ExpectRole != "" && el.Role != req.ExpectRole) || (req.ExpectName != "" && el.Name != req.ExpectName) {
			refuse(fmt.Sprintf("The element %s changed: it was %s %q and is now %s %q. Take a new snapshot.", req.Ref, req.ExpectRole, req.ExpectName, el.Role, el.Name))
			return
		}
		if el.Disabled && req.Action != "hover" && req.Action != "scroll" {
			refuse("The element " + req.Ref + " is disabled.")
			return
		}
		res.Confirmed = append(res.Confirmed, fmt.Sprintf("Found %s %s %q.", req.Ref, el.Role, el.Name))
	}
	sel := fmt.Sprintf(`[data-fettle-ref="%s"]`, req.Ref)
	var actErr error
	switch req.Action {
	case "click":
		actErr = chromedp.Run(ctx, chromedp.ScrollIntoView(sel, chromedp.ByQuery), chromedp.Click(sel, chromedp.ByQuery, chromedp.NodeVisible))
	case "type":
		actErr = chromedp.Run(ctx, chromedp.Focus(sel, chromedp.ByQuery),
			chromedp.Evaluate(fmt.Sprintf(`(() => { const e = document.querySelector(%q); if (e.isContentEditable) e.textContent = ''; else e.value = ''; })()`, sel), nil),
			chromedp.SendKeys(sel, req.Value, chromedp.ByQuery))
	case "select":
		var ok bool
		actErr = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => { const e = document.querySelector(%q); const o = [...e.options].find(o => o.value === %q || o.text.trim() === %q); if (!o) return false; e.value = o.value; e.dispatchEvent(new Event('input', {bubbles: true})); e.dispatchEvent(new Event('change', {bubbles: true})); return true; })()`, sel, req.Value, req.Value), &ok))
		if actErr == nil && !ok {
			refuse("The list " + req.Ref + " has no option " + req.Value + ".")
			return
		}
	case "check":
		want := req.Value != "false"
		if el.Checked == nil || *el.Checked != want {
			actErr = chromedp.Run(ctx, chromedp.Click(sel, chromedp.ByQuery, chromedp.NodeVisible))
		}
	case "scroll":
		if req.Ref != "" {
			actErr = chromedp.Run(ctx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))
		} else {
			actErr = chromedp.Run(ctx, chromedp.Evaluate(`window.scrollBy(0, window.innerHeight * 0.8)`, nil))
		}
	case "hover":
		actErr = chromedp.Run(ctx, chromedp.ScrollIntoView(sel, chromedp.ByQuery), chromedp.ActionFunc(func(ctx context.Context) error {
			var box []float64
			if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(() => { const r = document.querySelector(%q).getBoundingClientRect(); return [r.x + r.width/2, r.y + r.height/2]; })()`, sel), &box)); err != nil {
				return err
			}
			return input.DispatchMouseEvent(input.MouseMoved, box[0], box[1]).Do(ctx)
		}))
	case "press":
		tasks := chromedp.Tasks{}
		if req.Ref != "" {
			tasks = append(tasks, chromedp.Focus(sel, chromedp.ByQuery))
		}
		actErr = chromedp.Run(ctx, append(tasks, chromedp.KeyEvent(keyOf(req.Value)))...)
	}
	if actErr != nil {
		res.Uncertain = append(res.Uncertain, fmt.Sprintf("The %s may not have happened: %s. Take a new snapshot to check the page.", req.Action, cleanCDP(actErr)))
	} else {
		res.Confirmed = append(res.Confirmed, actionDone(req))
		// Let the page react, then wait for it to finish loading.
		settle, cancelSettle := context.WithTimeout(ctx, 5*time.Second)
		var state string
		_ = chromedp.Run(settle, chromedp.Sleep(300*time.Millisecond))
		for settle.Err() == nil {
			if err := chromedp.Run(settle, chromedp.Evaluate(`document.readyState`, &state)); err == nil && state == "complete" {
				break
			}
			_ = chromedp.Run(settle, chromedp.Sleep(200*time.Millisecond))
		}
		if state != "complete" {
			res.Uncertain = append(res.Uncertain, "The page did not finish loading in 5 seconds. Take a new snapshot to see the result.")
		}
		cancelSettle()
	}
	res.Dialogs = c.takeDialogs()
	for _, d := range res.Dialogs {
		res.Uncertain = append(res.Uncertain, "A dialog opened ("+d+"). Fettle did not accept it.")
	}
	if snap, err := c.snapshot(ctx); err == nil {
		res.Snapshot = &snap
		c.remember(snap)
	}
	writeJSON(w, http.StatusOK, res)
}

func actionDone(req client.ActRequest) string {
	switch req.Action {
	case "type":
		return fmt.Sprintf("Typed %d characters into %s.", len([]rune(req.Value)), req.Ref)
	case "select":
		return fmt.Sprintf("Selected %q in %s.", req.Value, req.Ref)
	case "press":
		return fmt.Sprintf("Pressed %s.", req.Value)
	case "scroll":
		return "Scrolled."
	case "check":
		if req.Value == "false" {
			return "Cleared " + req.Ref + "."
		}
		return "Checked " + req.Ref + "."
	}
	return fmt.Sprintf("Did %s on %s.", req.Action, req.Ref)
}

// keyOf maps key names to the characters that chromedp sends.
func keyOf(name string) string {
	switch strings.ToLower(name) {
	case "enter", "return":
		return "\r"
	case "tab":
		return "\t"
	case "escape", "esc":
		return "\u001b"
	case "backspace":
		return "\b"
	case "arrowdown", "down":
		return ""
	case "arrowup", "up":
		return ""
	}
	return name
}

func (s *Server) browserScreenshot(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 20*time.Second)
	defer cancel()
	var png []byte
	var u string
	if err := chromedp.Run(ctx, chromedp.Location(&u), chromedp.CaptureScreenshot(&png)); err != nil {
		s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "no screenshot: " + cleanCDP(err)})
		return
	}
	writeJSON(w, http.StatusOK, client.Screenshot{URL: u, PNG: base64.StdEncoding.EncodeToString(png)})
}

func (s *Server) browserDownloads(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	ents, err := os.ReadDir(c.downloads)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	out := []client.Download{}
	for _, e := range ents {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".crdownload") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		d := client.Download{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().UTC()}
		if !c.ephemeral {
			if rel, err := filepath.Rel(s.home, filepath.Join(c.downloads, e.Name())); err == nil {
				d.Path = filepath.ToSlash(rel)
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	writeJSON(w, http.StatusOK, out)
}

