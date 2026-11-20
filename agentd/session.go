package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"

	"github.com/alternayte/fettle/agentd/client"
)

// browserSession puts the cookies and the local storage of a sign-in that a person shared
// from their own browser into a context. The values never reach a log.
func (s *Server) browserSession(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	var req client.SessionRequest
	if err := decodeJSON(w, r, 4<<20, &req); err != nil {
		s.fail(w, "browser", err)
		return
	}
	params := make([]*network.CookieParam, 0, len(req.Cookies))
	for _, k := range req.Cookies {
		p, err := cookieParam(k)
		if err != nil {
			s.fail(w, "browser", err)
			return
		}
		params = append(params, p)
	}
	var origin *url.URL
	if len(req.Storage) > 0 {
		origin, err = url.Parse(req.Origin)
		if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" {
			s.fail(w, "browser", badRequest("origin must be an http or https origin"))
			return
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 60*time.Second)
	defer cancel()
	if len(params) > 0 {
		if err := chromedp.Run(ctx, network.SetCookies(params)); err != nil {
			s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the cookies were not set: " + cleanCDP(err)})
			return
		}
	}
	if origin != nil {
		// Local storage belongs to a document of its origin, so a second tab opens the origin,
		// writes the items and closes.
		tab, closeTab := chromedp.NewContext(ctx)
		items, _ := json.Marshal(req.Storage)
		js := fmt.Sprintf(`(() => { const o = %s; for (const k of Object.keys(o)) localStorage.setItem(k, o[k]); return true; })()`, items)
		var ok bool
		err := chromedp.Run(tab, chromedp.Navigate(origin.Scheme+"://"+origin.Host+"/"), chromedp.Evaluate(js, &ok))
		closeTab()
		if err != nil {
			s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the local storage was not set: " + cleanCDP(err)})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"cookies": len(params), "storage": len(req.Storage)})
}

func cookieParam(k client.SessionCookie) (*network.CookieParam, error) {
	host := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(k.Domain)), ".")
	if k.Name == "" && k.Value == "" || host == "" || strings.ContainsAny(host, "/:@ ") {
		return nil, badRequest("a cookie needs a name and a domain")
	}
	p := &network.CookieParam{Name: k.Name, Value: k.Value, Path: k.Path, Secure: k.Secure, HTTPOnly: k.HTTPOnly}
	if p.Path == "" {
		p.Path = "/"
	}
	if k.HostOnly {
		// A URL without a domain makes a host-only cookie, which __Host- cookies need.
		scheme := "http"
		if k.Secure {
			scheme = "https"
		}
		p.URL = scheme + "://" + host + p.Path
	} else {
		p.Domain = "." + host
	}
	switch k.SameSite {
	case "strict":
		p.SameSite = network.CookieSameSiteStrict
	case "lax":
		p.SameSite = network.CookieSameSiteLax
	case "no_restriction":
		p.SameSite = network.CookieSameSiteNone
	}
	if k.Expires > 0 {
		sec, frac := math.Modf(k.Expires)
		t := cdp.TimeSinceEpoch(time.Unix(int64(sec), int64(frac*1e9)))
		p.Expires = &t
	}
	return p, nil
}

// browserCookieHosts lists the hosts that hold cookies in a context, without names or values.
func (s *Server) browserCookieHosts(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 20*time.Second)
	defer cancel()
	var cookies []*network.Cookie
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = storage.GetCookies().Do(ctx)
		return err
	})); err != nil {
		s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the cookies could not be read: " + cleanCDP(err)})
		return
	}
	hosts := []string{}
	for _, k := range cookies {
		h := strings.TrimPrefix(k.Domain, ".")
		if h != "" && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	slices.Sort(hosts)
	writeJSON(w, http.StatusOK, client.CookieHosts{Hosts: hosts})
}

// browserSignOut removes the cookies of a domain and of its subdomains, and the stored data
// of their origins.
func (s *Server) browserSignOut(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserOf(r)
	if err != nil {
		s.fail(w, "browser", err)
		return
	}
	var req client.SignOutRequest
	if err := decodeJSON(w, r, 4<<10, &req); err != nil {
		s.fail(w, "browser", err)
		return
	}
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.Domain)), ".")
	if domain == "" || strings.ContainsAny(domain, "/:@ ") {
		s.fail(w, "browser", badRequest("domain is missing or not valid"))
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.tab, 30*time.Second)
	defer cancel()
	removed := 0
	err = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := storage.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		hosts := []string{domain}
		for _, k := range cookies {
			h := strings.TrimPrefix(k.Domain, ".")
			if h != domain && !strings.HasSuffix(h, "."+domain) {
				continue
			}
			if err := network.DeleteCookies(k.Name).WithDomain(k.Domain).WithPath(k.Path).Do(ctx); err != nil {
				return err
			}
			removed++
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
		for _, h := range hosts {
			for _, scheme := range []string{"https://", "http://"} {
				if err := storage.ClearDataForOrigin(scheme+h, "local_storage,indexeddb,cache_storage,service_workers,file_systems").Do(ctx); err != nil {
					return err
				}
			}
		}
		return nil
	}))
	if err != nil {
		s.fail(w, "browser", &httpError{status: http.StatusBadGateway, msg: "the sign-in was not removed: " + cleanCDP(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"cookies": removed})
}
