package agentd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/alternayte/fettle/agentd/client"
)

// teachBinding is the name of the CDP binding that the recorder script calls.
const teachBinding = "__fettleTeach"

// Limits of a recorded step.
const (
	teachURLMax    = 2000
	teachNameMax   = 200
	teachValueMax  = 500
	teachOutputMax = 2048
	teachStepsMax  = 2000
)

// teachRecorderJS records the person's actions on a page: clicks on interactive elements,
// changed fields, Enter in a field, and checked boxes. It names each target by the role and
// the accessible name that a snapshot gives it. It never sends the value of a password,
// card or one-time-code field: by type, autocomplete, name, id or label, and also after a
// show-password toggle changed the type (§13.3).
const teachRecorderJS = `(() => {
  if (window.__fettleTeachOn) return;
  window.__fettleTeachOn = true;
` + domHelpersJS + `  const send = (o) => {
    if (window.__fettleTeachOff || typeof window.` + teachBinding + ` !== 'function') return;
    o.url = location.href;
    try { window.` + teachBinding + `(JSON.stringify(o)); } catch (e) {}
  };
  const wasPassword = new WeakSet();
  const mark = (el) => {
    if (el && el.tagName && el.tagName.toLowerCase() === 'input' && (el.getAttribute('type') || '').toLowerCase() === 'password') wasPassword.add(el);
  };
  const markAll = (root) => { if (root && root.querySelectorAll) root.querySelectorAll('input[type=password]').forEach(mark); };
  const secretKind = (el) => {
    if (!el || !el.tagName) return '';
    const t = el.tagName.toLowerCase();
    if (t !== 'input' && t !== 'textarea' && !el.isContentEditable) return '';
    if ((el.getAttribute('type') || '').toLowerCase() === 'password' || wasPassword.has(el)) return 'password';
    const ac = (el.getAttribute('autocomplete') || '').toLowerCase();
    if (/(^|\s)(current-password|new-password)(\s|$)/.test(ac)) return 'password';
    if (/(^|\s)cc-/.test(ac)) return 'card';
    if (/(^|\s)one-time-code(\s|$)/.test(ac)) return 'one_time_code';
    const id = ((el.getAttribute('name') || '') + ' ' + (el.id || '') + ' ' + nameOf(el)).toLowerCase();
    if (/(passw|passwd|pwd|passcode|\bpin\b|secret)/.test(id)) return 'password';
    if (/(card.?num|ccnum|cc-?num|cvc|cvv|csc|security.?code|card.?code|expir)/.test(id)) return 'card';
    if (/(otp|one.?time|totp|mfa|2fa|two.?factor|verification.?code|auth.?code|sms.?code)/.test(id)) return 'one_time_code';
    return '';
  };
  const field = (el) => {
    if (!el || !el.tagName) return false;
    const t = el.tagName.toLowerCase();
    if (t === 'textarea' || el.isContentEditable) return true;
    if (t !== 'input') return false;
    const ty = (el.getAttribute('type') || 'text').toLowerCase();
    return !['submit','button','reset','image','checkbox','radio','file','hidden','range','color'].includes(ty);
  };
  const valueOf = (el) => el.isContentEditable ? el.textContent : el.value;
  const typed = (el) => {
    const v = valueOf(el);
    if (el.__fettleLast === v) return;
    el.__fettleLast = v;
    const k = secretKind(el);
    if (k) send({ action: 'type', role: roleOf(el), name: nameOf(el), secret: k });
    else send({ action: 'type', role: roleOf(el), name: nameOf(el), value: clip(v, 500) });
  };
  markAll(document);
  new MutationObserver((ms) => {
    for (const m of ms) {
      if (m.type === 'attributes' && (m.oldValue || '').toLowerCase() === 'password') wasPassword.add(m.target);
      if (m.type === 'attributes') mark(m.target);
      for (const n of m.addedNodes || []) { mark(n); markAll(n); }
    }
  }).observe(document, { subtree: true, childList: true, attributes: true, attributeFilter: ['type'], attributeOldValue: true });
  document.addEventListener('focusin', (e) => mark(e.target), true);
  document.addEventListener('pointerdown', (e) => mark(e.target), true);
  document.addEventListener('click', (e) => {
    const el = e.target && e.target.closest ? e.target.closest(sel) : null;
    if (!el || field(el)) return;
    const t = el.tagName.toLowerCase();
    if (t === 'select' || t === 'option') return;
    if (t === 'input' && ['checkbox','radio'].includes((el.getAttribute('type') || '').toLowerCase())) return;
    send({ action: 'click', role: roleOf(el), name: nameOf(el) });
  }, true);
  document.addEventListener('change', (e) => {
    const el = e.target;
    if (!el || !el.tagName) return;
    const t = el.tagName.toLowerCase();
    const ty = (el.getAttribute('type') || '').toLowerCase();
    if (t === 'select') { const o = el.options[el.selectedIndex]; send({ action: 'select', role: roleOf(el), name: nameOf(el), value: clip(o ? o.text : el.value, 500) }); return; }
    if (t === 'input' && (ty === 'checkbox' || ty === 'radio')) { send({ action: 'check', role: roleOf(el), name: nameOf(el), value: el.checked ? 'true' : 'false' }); return; }
    if (field(el)) typed(el);
  }, true);
  document.addEventListener('focusout', (e) => { if (e.target && e.target.isContentEditable) typed(e.target); }, true);
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' || !field(e.target) || e.target.tagName.toLowerCase() === 'textarea' || e.target.isContentEditable) return;
    typed(e.target);
    send({ action: 'press', role: roleOf(e.target), name: nameOf(e.target), value: 'Enter' });
  }, true);
})()`

// teachEvent is one browser event for the recorder: a binding call, a navigation that the
// page asked for (a link, a form or a script), or a main-frame navigation.
type teachEvent struct {
	payload   string
	requested bool
	navigated string
	frame     cdp.FrameID
}

// teachSession records the steps of one teach session: browser steps from the bot's
// browser context and command steps from the terminal.
type teachSession struct {
	id      string
	cwd     string // relative to the computer home
	browser *browserCtx
	script  page.ScriptIdentifier

	mu      sync.Mutex
	steps   []client.TeachStep
	next    int
	events  []teachEvent
	wake    chan struct{}
	stopped chan struct{}
	done    chan struct{}
	ptys    map[int]func()
	ptyNext int
	// pageNav is set while a navigation that the page asked for is under way.
	pageNav bool
	// mainFrame is the top frame of the tab.
	mainFrame cdp.FrameID
}

type teachSessions struct {
	mu sync.Mutex
	m  map[string]*teachSession
}

func newTeachSessions() *teachSessions { return &teachSessions{m: map[string]*teachSession{}} }

func (ts *teachSessions) get(id string) (*teachSession, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.m[id]
	return t, ok
}

func (ts *teachSessions) stopAll() {
	ts.mu.Lock()
	all := ts.m
	ts.m = map[string]*teachSession{}
	ts.mu.Unlock()
	for _, t := range all {
		t.stop()
	}
}

// queue takes an event from the CDP listener. The listener must not block, so the worker
// handles the events in their order.
func (t *teachSession) queue(ev teachEvent) {
	t.mu.Lock()
	t.events = append(t.events, ev)
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// add appends a step with the next seq.
func (t *teachSession) add(st client.TeachStep) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.steps) >= teachStepsMax {
		return
	}
	t.next++
	st.Seq = t.next
	st.At = time.Now().UTC()
	t.steps = append(t.steps, st)
}

// work turns browser events into steps, one at a time, with a screenshot of each.
func (t *teachSession) work() {
	defer close(t.done)
	for {
		select {
		case <-t.stopped:
			return
		case <-t.wake:
		}
		for {
			t.mu.Lock()
			if len(t.events) == 0 {
				t.mu.Unlock()
				break
			}
			ev := t.events[0]
			t.events = t.events[1:]
			t.mu.Unlock()
			if st, ok := t.browserStep(ev); ok {
				st.Screenshot = t.shot()
				t.add(st)
			}
		}
	}
}

// browserStep reads one event. A navigation becomes a step only when the page did not ask
// for it; a click that opens a page is a click step already.
func (t *teachSession) browserStep(ev teachEvent) (client.TeachStep, bool) {
	if ev.requested {
		if t.mainFrame == "" || ev.frame == t.mainFrame {
			t.pageNav = true
		}
		return client.TeachStep{}, false
	}
	if ev.navigated != "" {
		t.mainFrame = ev.frame
		// The page asks for its own navigations; one that it did not ask for comes from the
		// address bar, a reload or the back button.
		byPage := t.pageNav
		t.pageNav = false
		web := strings.HasPrefix(ev.navigated, "http://") || strings.HasPrefix(ev.navigated, "https://")
		if byPage || !web {
			return client.TeachStep{}, false
		}
		return client.TeachStep{Kind: "browser", Action: "navigate", URL: clipText(ev.navigated, teachURLMax)}, true
	}
	var in struct {
		Action string `json:"action"`
		Role   string `json:"role"`
		Name   string `json:"name"`
		Value  string `json:"value"`
		Secret string `json:"secret"`
		URL    string `json:"url"`
	}
	if json.Unmarshal([]byte(ev.payload), &in) != nil {
		return client.TeachStep{}, false
	}
	if !slices.Contains([]string{"click", "type", "select", "check", "press"}, in.Action) {
		return client.TeachStep{}, false
	}
	st := client.TeachStep{Kind: "browser", Action: in.Action, URL: clipText(in.URL, teachURLMax), Role: clipText(in.Role, 40), Name: clipText(in.Name, teachNameMax)}
	switch in.Secret {
	case "":
		st.Value = clipText(in.Value, teachValueMax)
	case client.SecretPassword, client.SecretCard, client.SecretOneTimeCode:
		st.Secret = in.Secret
	default:
		st.Secret = client.SecretPassword
	}
	return st, true
}

// shot returns a JPEG of the page, base64, or "" when the page cannot be captured. A page
// that navigates cannot be captured for a moment, so it tries again.
func (t *teachSession) shot() string {
	for try := 0; try < 4; try++ {
		if try > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(t.browser.tab, 5*time.Second)
		var b []byte
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			b, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(60).Do(ctx)
			return err
		}))
		cancel()
		if err == nil && len(b) > 0 {
			return base64.StdEncoding.EncodeToString(b)
		}
	}
	return ""
}

// take returns the steps after seq after and forgets the steps up to after.
func (t *teachSession) take(after int) []client.TeachStep {
	t.mu.Lock()
	defer t.mu.Unlock()
	keep := t.steps[:0]
	out := []client.TeachStep{}
	for _, st := range t.steps {
		if st.Seq > after {
			keep = append(keep, st)
			out = append(out, st)
		}
	}
	t.steps = keep
	return out
}

// stop detaches the recorder from the browser and ends the terminals.
func (t *teachSession) stop() {
	select {
	case <-t.stopped:
		return
	default:
	}
	close(t.stopped)
	<-t.done
	if t.browser != nil {
		t.browser.teach.CompareAndSwap(t, nil)
		ctx, cancel := context.WithTimeout(t.browser.tab, 5*time.Second)
		_ = chromedp.Run(ctx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				if t.script != "" {
					_ = page.RemoveScriptToEvaluateOnNewDocument(t.script).Do(ctx)
				}
				return runtime.RemoveBinding(teachBinding).Do(ctx)
			}),
			chromedp.Evaluate(`window.__fettleTeachOff = true`, nil))
		cancel()
	}
	t.mu.Lock()
	kills := make([]func(), 0, len(t.ptys))
	for _, k := range t.ptys {
		kills = append(kills, k)
	}
	t.mu.Unlock()
	for _, k := range kills {
		k()
	}
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func (s *Server) teachStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !screenID.MatchString(id) {
		s.fail(w, "teach", badRequest("teach id %q is not valid", id))
		return
	}
	var req client.TeachStart
	if err := decodeJSON(w, r, 64<<10, &req); err != nil {
		s.fail(w, "teach", err)
		return
	}
	rel, err := relPath(req.Cwd, true)
	if err != nil {
		s.fail(w, "teach", err)
		return
	}
	if st, err := s.root.Stat(rel); err != nil || !st.IsDir() {
		s.fail(w, "teach", badRequest("%s: the terminal folder does not exist", req.Cwd))
		return
	}
	s.teach.mu.Lock()
	defer s.teach.mu.Unlock()
	if _, ok := s.teach.m[id]; ok {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "created": false})
		return
	}
	t := &teachSession{id: id, cwd: fsPath(rel), wake: make(chan struct{}, 1), stopped: make(chan struct{}), done: make(chan struct{}), ptys: map[int]func(){}}
	if req.Browser != "" {
		s.browsers.mu.Lock()
		c, ok := s.browsers.m[req.Browser]
		s.browsers.mu.Unlock()
		if !ok {
			s.fail(w, "teach", &httpError{status: http.StatusNotFound, msg: "no browser context " + req.Browser})
			return
		}
		if !c.teach.CompareAndSwap(nil, t) {
			s.fail(w, "teach", &httpError{status: http.StatusConflict, msg: "another teach session records this browser"})
			return
		}
		t.browser = c
		ctx, cancel := context.WithTimeout(c.tab, 15*time.Second)
		var cur string
		err := chromedp.Run(ctx,
			runtime.AddBinding(teachBinding),
			chromedp.ActionFunc(func(ctx context.Context) error {
				id, err := page.AddScriptToEvaluateOnNewDocument(teachRecorderJS).Do(ctx)
				t.script = id
				return err
			}),
			chromedp.Evaluate(teachRecorderJS, nil),
			chromedp.Location(&cur),
			chromedp.ActionFunc(func(ctx context.Context) error {
				tree, err := page.GetFrameTree().Do(ctx)
				if err == nil && tree != nil && tree.Frame != nil {
					t.mainFrame = tree.Frame.ID
				}
				return nil
			}))
		cancel()
		if err != nil {
			c.teach.CompareAndSwap(t, nil)
			s.fail(w, "teach", &httpError{status: http.StatusBadGateway, msg: "the recorder did not start in the browser: " + cleanCDP(err)})
			return
		}
		// The page that is open when teaching starts is where the task starts.
		if strings.HasPrefix(cur, "http://") || strings.HasPrefix(cur, "https://") {
			t.add(client.TeachStep{Kind: "browser", Action: "navigate", URL: clipText(cur, teachURLMax), Screenshot: t.shot()})
		}
	}
	go t.work()
	s.teach.m[id] = t
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "created": true})
}

func (s *Server) teachOf(w http.ResponseWriter, r *http.Request) (*teachSession, bool) {
	t, ok := s.teach.get(r.PathValue("id"))
	if !ok {
		s.fail(w, "teach", &httpError{status: http.StatusNotFound, msg: "no teach session " + r.PathValue("id")})
	}
	return t, ok
}

func afterOf(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("after"))
	return n
}

func (s *Server) teachSteps(w http.ResponseWriter, r *http.Request) {
	t, ok := s.teachOf(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, client.TeachStepsReply{Steps: t.take(afterOf(r))})
}

func (s *Server) teachStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.teach.mu.Lock()
	t, ok := s.teach.m[id]
	delete(s.teach.m, id)
	s.teach.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusOK, client.TeachStepsReply{Steps: []client.TeachStep{}})
		return
	}
	t.stop()
	writeJSON(w, http.StatusOK, client.TeachStepsReply{Steps: t.take(afterOf(r))})
}

// relCwd returns an absolute folder of the computer as a path relative to the home, or ""
// when it is outside the home.
func (s *Server) relCwd(abs string) string {
	rel, err := filepath.Rel(s.home, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// teachMark starts a shell integration mark of the teach terminal.
const teachMark = "\x1b]6973;"

// ptyRecorder reads the output of the teach terminal and makes a command step for each
// command between the start mark and the next prompt mark. It never sees the person's
// input; it only knows that input arrived while echo was off.
type ptyRecorder struct {
	s *Server
	t *teachSession

	mu       sync.Mutex
	cwd      string // relative to the computer home
	in       bool
	cmd      string
	out      []byte
	secretIn bool
	pend     []byte
}

// feed reads terminal output.
func (p *ptyRecorder) feed(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data := append(p.pend, b...)
	p.pend = nil
	for len(data) > 0 {
		i := strings.Index(string(data), teachMark)
		if i < 0 {
			keep := partialSuffix(data, teachMark)
			p.output(data[:len(data)-keep])
			p.pend = append([]byte(nil), data[len(data)-keep:]...)
			return
		}
		p.output(data[:i])
		rest := data[i+len(teachMark):]
		j := strings.IndexByte(string(rest), 0x07)
		if j < 0 {
			if len(data)-i < 16<<10 {
				p.pend = append([]byte(nil), data[i:]...)
			}
			return
		}
		p.mark(string(rest[:j]))
		data = rest[j+1:]
	}
}

// partialSuffix returns the length of the longest end of b that starts mark.
func partialSuffix(b []byte, mark string) int {
	for n := min(len(b), len(mark)-1); n > 0; n-- {
		if strings.HasPrefix(mark, string(b[len(b)-n:])) {
			return n
		}
	}
	return 0
}

func (p *ptyRecorder) output(b []byte) {
	if p.in && len(p.out) < 64<<10 {
		p.out = append(p.out, b[:min(len(b), 64<<10-len(p.out))]...)
	}
}

func (p *ptyRecorder) mark(m string) {
	switch {
	case m == "C" || strings.HasPrefix(m, "C;"):
		p.in, p.out, p.secretIn = true, p.out[:0], false
		p.cmd = clipText(decodeB64(strings.TrimPrefix(strings.TrimPrefix(m, "C"), ";")), 4000)
	case strings.HasPrefix(m, "D;"):
		parts := strings.SplitN(m[2:], ";", 2)
		if len(parts) != 2 {
			return
		}
		if p.in && p.cmd != "" {
			ec, _ := strconv.Atoi(parts[0])
			st := client.TeachStep{Kind: "command", Command: p.cmd, Cwd: p.cwd, ExitCode: ec, Output: clipText(terminalText(p.out), teachOutputMax)}
			if p.secretIn {
				st.Secret = client.SecretEchoOff
			}
			p.t.add(st)
		}
		p.in, p.out, p.secretIn, p.cmd = false, nil, false, ""
		if rel := p.s.relCwd(decodeB64(parts[1])); rel != "" {
			p.cwd = rel
		}
	}
}

// secret notes that the person typed while the terminal did not show the input.
func (p *ptyRecorder) secret() {
	p.mu.Lock()
	if p.in {
		p.secretIn = true
	}
	p.mu.Unlock()
}

var (
	ansiOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	ansiCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiEsc = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

// terminalText returns terminal output as plain text: no escape sequences, and \n line ends.
func terminalText(b []byte) string {
	s := string(b)
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiCSI.ReplaceAllString(s, "")
	s = ansiEsc.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ToValidUTF8(s, "")
}

// decodeB64 reads a base64 field of a shell mark.
func decodeB64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}
