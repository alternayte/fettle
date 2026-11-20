//go:build desktop

// Command fettle-desktop is the desktop app (§15.1): a Wails v3 window and tray icon
// around the Fettle server, which it runs in process. The window loads the server's UI over
// HTTP with the local token (C-06); the app binds no method that returns app data (SI-16).
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/desktop"
)

// Set by the release build.
var (
	version       = "dev"
	updateKey     = "" // base64 Ed25519 public key of the release feed
	computerImage = "" // the computer image of this release
)

type shell struct {
	data  string
	cfg   desktop.Config
	wails *application.App
	win   *application.WebviewWindow
	notes *notifications.NotificationService

	mu      sync.Mutex
	server  *app.App
	base    string // the UI origin that the window loads
	windows int
	// pendingUpdate shows the update question when the window shows again.
	pendingUpdate func()
	// hidden is true while the window is closed to the tray. Wails reports occlusion, not
	// this state: a locked screen reads as not visible.
	hidden bool
	// openURL opens a link of another origin in the system browser.
	openURL func(u string) error
	// asked is called when the update dialog shows (the smoke build answers it).
	asked func(install func())
	// wrap, when set, wraps the server's handler (the smoke build records requests).
	wrap func(http.Handler) http.Handler
}

// smoke, when set by the smoke build, starts the test control after the window exists.
var smoke func(s *shell)

// smokeEarly, when set by the smoke build, sets its hooks before the server starts.
var smokeEarly func(s *shell)

func main() {
	// The release check reads the image that this build names, without a window.
	if len(os.Args) == 2 && os.Args[1] == "--computer-image" {
		fmt.Println(app.BuiltinImage(computerImage))
		return
	}
	data := os.Getenv("FETTLE_DATA")
	if data == "" {
		data = app.DefaultDataDir(app.ModeLocal)
	}
	cfg, err := desktop.Load(data)
	if err != nil {
		slog.Error("read the desktop settings", "error", err)
	}
	s := &shell{data: data, cfg: cfg, notes: notifications.New()}
	s.openURL = func(u string) error { return s.wails.Browser.OpenURL(u) }
	if smokeEarly != nil {
		smokeEarly(s)
	}
	s.wails = application.New(application.Options{
		Name:        "Fettle",
		Description: "Bots that work on computers and ask before they act",
		// The only bound service sends notifications; no bound method returns app data (SI-16).
		Services:   []application.Service{application.NewService(s.notes)},
		Mac:        application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		OnShutdown: s.stop,
	})
	if err := s.start(); err != nil {
		slog.Error("start", "error", err)
		os.Exit(1)
	}
	s.win = s.wails.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "Fettle", Width: 1280, Height: 840, MinWidth: 360, MinHeight: 480,
		URL: s.entryURL(),
	})
	// Closing the window keeps the server and routines running in the tray (§15.1).
	s.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		s.win.Hide()
		s.mu.Lock()
		s.hidden = true
		s.mu.Unlock()
	})
	s.tray()
	s.notes.OnNotificationResponse(func(r notifications.NotificationResult) {
		if u, ok := r.Response.UserInfo["url"].(string); ok && strings.HasPrefix(u, "/") {
			s.show(u)
		}
	})
	if smoke != nil {
		smoke(s)
	}
	go s.updates()
	if err := s.wails.Run(); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}

// start runs the local server, unless the app connects to a remote server.
func (s *shell) start() error {
	if s.cfg.Mode == desktop.ModeRemote && s.cfg.ServerURL != "" {
		s.base = s.cfg.ServerURL
		return nil
	}
	cfg := app.ConfigFromEnv()
	cfg.Mode, cfg.Data, cfg.Version, cfg.ComputerImage = app.ModeLocal, s.data, version, computerImage
	// The desktop user cannot set environment variables, so the app reads the certificates
	// and the proxy of this computer (§15.1).
	cfg.HostSettings = true
	cfg.Desktop = true
	cfg.Addr = freeAddr(cfg.Addr)
	a, err := app.New(context.Background(), &cfg)
	if err != nil {
		return err
	}
	a.Desktop = &app.Desktop{
		OpenURL:    func(u string) error { return s.openURL(u) },
		OpenWindow: s.openWindow,
		Connect:    s.connect,
		Notify:     s.notify,
		FirstRun: func() bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.cfg.Mode == desktop.ModeUnset
		},
		KeepLocal: func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.cfg.Mode = desktop.ModeLocal
			return desktop.Save(s.data, s.cfg)
		},
	}
	h := a.Handler()
	if s.wrap != nil {
		h = s.wrap(h)
	}
	if err := a.Start(context.Background(), h); err != nil {
		_ = a.Shutdown(context.Background())
		return err
	}
	s.mu.Lock()
	s.server, s.base = a, a.URL()
	s.mu.Unlock()
	return nil
}

// freeAddr keeps the usual local address when it is free, else takes any free port.
func freeAddr(addr string) string {
	if addr == "" {
		addr = "127.0.0.1:7420"
	}
	l, err := net.Listen("tcp", addr)
	if err == nil {
		_ = l.Close()
		return addr
	}
	return "127.0.0.1:0"
}

// entryURL is the first page: the local UI with the token in the hash, which the UI trades
// for a cookie and removes from the address (C-06), or the remote server.
func (s *shell) entryURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return s.base
	}
	return s.base + "/#token=" + s.server.LocalToken
}

// openWindow shows a UI path (a bot's screen) in a window of its own (C-06).
func (s *shell) openWindow(path, title string) error {
	s.mu.Lock()
	base := s.base
	s.windows++
	n := s.windows
	s.mu.Unlock()
	if title == "" {
		title = "Fettle screen"
	}
	application.InvokeSync(func() {
		w := s.wails.Window.NewWithOptions(application.WebviewWindowOptions{
			Name: fmt.Sprintf("screen-%d", n), Title: title, Width: 1300, Height: 880, MinWidth: 360, MinHeight: 300, URL: base + path,
		})
		w.Show()
	})
	return nil
}

func (s *shell) show(path string) {
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()
	if path != "" {
		s.win.SetURL(base + path)
	}
	s.win.Show()
	s.win.Focus()
	s.mu.Lock()
	s.hidden = false
	present := s.pendingUpdate
	s.pendingUpdate = nil
	s.mu.Unlock()
	if present != nil {
		go present()
	}
}

// connect switches to a remote server: it saves the choice, stops the local server and
// loads the remote UI (§15.1, "Connect to a server").
func (s *shell) connect(serverURL string) error {
	s.mu.Lock()
	s.cfg.Mode, s.cfg.ServerURL = desktop.ModeRemote, serverURL
	err := desktop.Save(s.data, s.cfg)
	srv := s.server
	s.server, s.base = nil, serverURL
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	application.InvokeSync(func() { s.win.SetURL(serverURL) })
	return nil
}

func (s *shell) stop() {
	s.mu.Lock()
	srv := s.server
	s.server = nil
	s.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (s *shell) notify(title, body, url string) {
	_ = s.notes.SendNotification(notifications.NotificationOptions{
		ID: fmt.Sprintf("fettle-%d", time.Now().UnixNano()), Title: title, Body: body,
		Data: map[string]any{"url": url},
	})
}

func (s *shell) tray() {
	t := s.wails.SystemTray.New()
	t.SetIcon(trayIcon())
	if runtime.GOOS == "darwin" {
		t.SetTemplateIcon(trayIcon())
	}
	m := application.NewMenu()
	m.Add("Open Fettle").OnClick(func(*application.Context) { s.show("") })
	on, _ := s.wails.Autostart.IsEnabled()
	login := m.AddCheckbox("Start at login", on)
	login.OnClick(func(c *application.Context) {
		var err error
		if c.ClickedMenuItem().Checked() {
			err = s.wails.Autostart.Enable()
		} else {
			err = s.wails.Autostart.Disable()
		}
		if err != nil {
			slog.Warn("start at login", "error", err)
		}
	})
	m.AddSeparator()
	m.Add("Quit Fettle").OnClick(func(*application.Context) { s.wails.Quit() })
	t.SetMenu(m)
	t.OnClick(func() { s.show("") })
}

// updates checks the release feed at start and every day. A newer signed package installs
// only after the user agrees, and the app restarts (§15.1).
func (s *shell) updates() {
	key, _ := base64.StdEncoding.DecodeString(updateKey)
	if k := os.Getenv("FETTLE_UPDATE_KEY"); k != "" {
		key, _ = base64.StdEncoding.DecodeString(k)
	}
	feed := s.cfg.UpdateFeed
	if f := os.Getenv("FETTLE_UPDATE_FEED"); f != "" {
		feed = f
	}
	if feed == "" {
		feed = desktop.DefaultFeed
	}
	u := &desktop.Updater{Feed: feed, Current: version, Key: ed25519.PublicKey(key), HTTP: &http.Client{Timeout: 10 * time.Minute}}
	for {
		s.checkUpdate(u)
		time.Sleep(24 * time.Hour)
	}
}

func (s *shell) checkUpdate(u *desktop.Updater) {
	ctx := context.Background()
	m, p, err := u.Check(ctx)
	if err != nil || m == nil || p == nil {
		if err != nil {
			slog.Info("update check", "error", err)
		}
		return
	}
	file, err := u.Download(ctx, m, p, filepath.Join(s.data, "updates"))
	if err != nil {
		slog.Warn("update download", "error", err)
		return
	}
	// The question is a sheet on the window: a modal alert would stop the main thread and
	// with it every window. A hidden window gets a notification, and the sheet waits for it.
	present := func() {
		application.InvokeSync(func() {
			d := s.wails.Dialog.Question().SetTitle("Fettle " + m.Version + " is ready").
				SetMessage("Install the update and restart Fettle now? Running work continues after the restart.")
			yes := d.AddButton("Install and restart")
			yes.IsDefault = true
			yes.OnClick(func() { go s.install(file) })
			later := d.AddButton("Later")
			later.IsCancel = true
			d.AttachToWindow(s.win).Show()
		})
		if s.asked != nil {
			s.asked(func() { go s.install(file) })
		}
	}
	s.mu.Lock()
	hidden := s.hidden
	if hidden {
		s.pendingUpdate = present
	}
	s.mu.Unlock()
	if !hidden {
		present()
		return
	}
	s.notify("Fettle "+m.Version+" is ready", "Open Fettle to install the update.", "/")
}

func (s *shell) install(file string) {
	target, err := installTarget()
	if err == nil {
		err = desktop.Install(file, target)
	}
	if err != nil {
		slog.Error("update install", "error", err)
		return
	}
	s.stop()
	// The new program starts with this process's environment (its data folder and flags).
	exe := target
	if strings.HasSuffix(target, ".app") {
		exe = filepath.Join(target, "Contents", "MacOS", filepath.Base(os.Args[0]))
	}
	cmd := exec.Command(exe)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		slog.Error("restart after the update", "error", err)
	}
	// AppKit does not terminate while a sheet is open. The server is stopped and the new
	// version runs, so this process may end without the AppKit shutdown.
	go func() {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}()
	s.wails.Quit()
}

// installTarget is the .app bundle on macOS, else the program file.
func installTarget() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		return exe[:i+len(".app")], nil
	}
	if runtime.GOOS == "darwin" {
		return "", errors.New("the app does not run from an .app bundle")
	}
	if ai := os.Getenv("APPIMAGE"); ai != "" {
		return ai, nil
	}
	return exe, nil
}

// trayIcon draws the tray mark: a rounded square with an F, in black on clear, so macOS
// can tint it as a template image.
func trayIcon() []byte {
	const n = 44
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	ink := color.NRGBA{A: 255}
	fill := func(x0, y0, x1, y1 int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.Set(x, y, ink)
			}
		}
	}
	// Frame.
	fill(4, 4, 40, 7)
	fill(4, 37, 40, 40)
	fill(4, 4, 7, 40)
	fill(37, 4, 40, 40)
	// F.
	fill(15, 12, 19, 32)
	fill(15, 12, 30, 16)
	fill(15, 20, 27, 24)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
