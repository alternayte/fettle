package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Desktop is what the desktop app does for the local server it runs in process (§15.1).
// The UI reaches it over HTTP like any other route; the desktop shell binds no methods
// that return app data (C-06, SI-16).
type Desktop struct {
	// OpenURL opens a URL of another origin in the system browser.
	OpenURL func(u string) error
	// Connect switches the app to a remote server: it stores the URL, stops the local
	// server and loads the remote UI.
	Connect func(serverURL string) error
	// Notify shows a native notification; url is the card or run path.
	Notify func(title, body, url string)
	// FirstRun reports that the user has not chosen this computer or a server yet.
	FirstRun func() bool
	// KeepLocal records the choice "This computer".
	KeepLocal func() error
	// OpenWindow opens a path of the UI in a window of its own (screens, C-06).
	OpenWindow func(path, title string) error
}

// checkServer reports whether a URL answers like a Fettle server.
func checkServer(ctx context.Context, client *http.Client, raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("give the full address of the server, for example https://fettle.example.com")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return "", errors.New("a remote server needs https")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String()+APIPrefix+"/instance", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the server does not answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the address does not answer like a Fettle server (status %d)", resp.StatusCode)
	}
	return u.String(), nil
}

func (a *App) registerDesktop(api huma.API) {
	huma.Register(api, op("desktop-open", http.MethodPost, "/desktop/open", "Open a link of another origin in the system browser (desktop app only)", "desktop"), func(ctx context.Context, in *struct {
		Body struct {
			URL string `json:"url" maxLength:"4000"`
		}
	}) (*struct{}, error) {
		if a.Desktop == nil || a.Desktop.OpenURL == nil {
			return nil, huma.Error404NotFound("this server does not run in the desktop app")
		}
		u, err := url.Parse(in.Body.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") {
			return nil, huma.Error422UnprocessableEntity("only http, https and mailto links open in the browser")
		}
		return nil, a.Desktop.OpenURL(u.String())
	})

	huma.Register(api, op("desktop-window", http.MethodPost, "/desktop/window", "Open a screen of a bot in a window of its own (desktop app only)", "desktop"), func(ctx context.Context, in *struct {
		Body struct {
			Path  string `json:"path" maxLength:"500" doc:"A UI path that starts with /screen/."`
			Title string `json:"title,omitempty" maxLength:"100"`
		}
	}) (*struct{}, error) {
		if a.Desktop == nil || a.Desktop.OpenWindow == nil {
			return nil, huma.Error404NotFound("this server does not run in the desktop app")
		}
		if !strings.HasPrefix(in.Body.Path, "/screen/") || strings.Contains(in.Body.Path, "//") {
			return nil, huma.Error422UnprocessableEntity("only screens open in their own window")
		}
		return nil, a.Desktop.OpenWindow(in.Body.Path, in.Body.Title)
	})

	huma.Register(api, op("desktop-local", http.MethodPost, "/desktop/local", "Use this computer: the app runs Fettle here (desktop app only)", "desktop"), func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		if a.Desktop == nil || a.Desktop.KeepLocal == nil {
			return nil, huma.Error404NotFound("this server does not run in the desktop app")
		}
		return nil, a.Desktop.KeepLocal()
	})

	huma.Register(api, op("desktop-connect", http.MethodPost, "/desktop/connect", "Use a remote Fettle server instead of this computer (desktop app only)", "desktop"), func(ctx context.Context, in *struct {
		Body struct {
			URL string `json:"url" maxLength:"2000"`
		}
	}) (*struct {
		Body struct {
			URL string `json:"url"`
		}
	}, error) {
		if a.Desktop == nil || a.Desktop.Connect == nil {
			return nil, huma.Error404NotFound("this server does not run in the desktop app")
		}
		u, err := checkServer(ctx, a.HTTP, in.Body.URL)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		out := &struct {
			Body struct {
				URL string `json:"url"`
			}
		}{}
		out.Body.URL = u
		// The switch stops this server, so it runs after the reply.
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := a.Desktop.Connect(u); err != nil {
				a.Log.Warn("desktop connect", "error", err)
			}
		}()
		return out, nil
	})
}
