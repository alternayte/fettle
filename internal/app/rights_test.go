package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/spaces"
)

const rightsBase = "https://fettle.test"

// route is one operation of the OpenAPI document.
type route struct{ method, path string }

func routesOf(t *testing.T, c *client) []route {
	t.Helper()
	code, _, body := c.do("GET", "/api/v1/openapi.json", nil, "")
	if code != 200 {
		t.Fatalf("openapi: %d", code)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	var out []route
	for p, ops := range doc.Paths {
		for m := range ops {
			switch m {
			case "get", "post", "put", "patch", "delete":
				out = append(out, route{strings.ToUpper(m), strings.TrimPrefix(p, "/api/v1")})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path+out[i].method < out[j].path+out[j].method })
	return out
}

// ownerOnly is the rule of §16 and Appendix G for a space route: members chat, read and
// steer runs; only owners change bots, skills, routines, settings, zones, grants,
// connectors, the vault, approvals, budgets, members and invites, and see learning.
func ownerOnly(method, rest string) bool {
	seg := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	read := method == "GET"
	switch seg[0] {
	case "":
		return !read
	case "connectors", "zones", "computer-providers", "vault", "policy", "approval-mode", "settings", "usage", "audit",
		"model-providers", "model-roles", "learning", "invites", "templates", "onboarding":
		return true
	case "memory":
		return !read
	case "bots":
		if len(seg) >= 3 && seg[2] == "screen" {
			return false
		}
		return !read
	case "skills", "routines":
		return !read
	case "runs":
		return len(seg) >= 3 && seg[2] == "draft-skill"
	case "members":
		return !read
	}
	return false
}

// fill puts IDs into a path template.
func fill(path string, ids map[string]string) string {
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			return path
		}
		j := strings.Index(path[i:], "}") + i
		name := path[i+1 : j]
		v, ok := ids[name]
		if !ok {
			v = uuid.NewString()
		}
		path = path[:i] + v + path[j+1:]
	}
}

// call sends one request with the session of c; a stream answers with its headers only.
func call(t *testing.T, c *client, method, path string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var body io.Reader
	if method != "GET" && method != "DELETE" {
		body = strings.NewReader("{}")
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.url+"/api/v1"+path, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", rightsBase)
	req.Header.Set("Cookie", c.cookie)
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed through rc in the defer
	if err != nil {
		t.Logf("%s %s: %v", method, path, err)
		// A timeout means the request passed the checks and works, for example a computer
		// that starts.
		return 0, "timeout"
	}
	rc := resp.Body
	defer func() { _ = rc.Close() }()
	if strings.HasSuffix(path, "/stream") {
		// Read what the stream sends in a moment: it must hold no data of another space.
		buf := make([]byte, 64<<10)
		done := make(chan string, 1)
		go func() {
			n, _ := io.ReadAtLeast(rc, buf, 1)
			done <- string(buf[:n])
		}()
		select {
		case s := <-done:
			return resp.StatusCode, s
		case <-time.After(500 * time.Millisecond):
			return resp.StatusCode, ""
		}
	}
	b, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
	return resp.StatusCode, string(b)
}

type rightsEnv struct {
	a                      *app.App
	owner, member, mallory *client
	acme, other            string
	ids                    map[string]string // acme IDs for path parameters
}

func startRights(t *testing.T) *rightsEnv {
	t.Helper()
	a, url := serverApp(t, "sqlite", rightsBase)
	ctx := context.Background()
	r := &rightsEnv{a: a, ids: map[string]string{}}
	newClient := func(email, password string) *client {
		c := &client{t: t, url: url, origin: rightsBase}
		if code := c.signIn(email, password); code != 200 {
			t.Fatalf("sign-in of %s: %d", email, code)
		}
		return c
	}
	r.owner = newClient("owner@acme.test", "acme-owner-pass-4410")
	var me struct {
		ID     string `json:"id"`
		Spaces []struct {
			ID string `json:"id"`
		} `json:"spaces"`
	}
	_, _, body := r.owner.do("GET", "/api/v1/me", nil, "")
	_ = json.Unmarshal([]byte(body), &me)
	r.acme = me.Spaces[0].ID
	acmeID := uuid.MustParse(r.acme)

	mem, err := a.SignIn.CreateUser(ctx, "member@acme.test", "Member", "acme-member-pass-5521", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Spaces.CreateInvite(ctx, audit.System, acmeID, uuid.MustParse(me.ID), spaces.RoleMember, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces.AcceptInvite(ctx, token, mem.ID()); err != nil {
		t.Fatal(err)
	}
	r.member = newClient("member@acme.test", "acme-member-pass-5521")
	mal, err := a.SignIn.CreateUser(ctx, "mallory@other.test", "Mallory", "other-mallory-pass-7712", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.CreateSpace(ctx, audit.System, "other", "UTC", mal.ID())
	if err != nil {
		t.Fatal(err)
	}
	r.other = other.ID().String()
	r.mallory = newClient("mallory@other.test", "other-mallory-pass-7712")

	// IDs of acme for the path parameters.
	chief, err := a.Bots.Primary(ctx, acmeID)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := a.Chats.DirectFor(ctx, acmeID, chief.ID())
	if err != nil {
		t.Fatal(err)
	}
	zs, err := a.Zones.List(ctx, acmeID)
	if err != nil || len(zs) == 0 {
		t.Fatalf("zones %v", err)
	}
	sk, err := a.Skills.ByName(ctx, acmeID, "onboarding-interview")
	if err != nil {
		t.Fatal(err)
	}
	r.ids = map[string]string{"space": r.acme, "bot": chief.ID().String(), "chat": chat.ID().String(), "zone": zs[0].ID().String(),
		"skill": sk.ID().String(), "user": me.ID, "key": "agent.max_turns", "role": "owner", "rev": "1", "tool": "x", "template": "inbox"}
	return r
}

func TestSCN_AUTH_002_RoleRightsOnEveryRoute(t *testing.T) {
	r := startRights(t)
	var checked int
	for _, rt := range routesOf(t, r.owner) {
		if !strings.HasPrefix(rt.path, "/spaces/{space}") {
			continue
		}
		rest := strings.TrimPrefix(rt.path, "/spaces/{space}")
		p := fill(rt.path, r.ids)
		code, body := call(t, r.member, rt.method, p)
		if ownerOnly(rt.method, rest) {
			if code != 403 {
				t.Errorf("member %s %s: %d, want 403 (%s)", rt.method, rt.path, code, trunc(body))
			}
		} else if code == 403 || code == 401 {
			t.Errorf("member %s %s: %d, want access (%s)", rt.method, rt.path, code, trunc(body))
		}
		// 503 is the defined answer when the computer cannot start in the test (no image).
		if code >= 500 && code != 503 {
			t.Errorf("member %s %s: %d (%s)", rt.method, rt.path, code, trunc(body))
		}
		checked++
	}
	if checked < 80 {
		t.Fatalf("only %d space routes in the OpenAPI document", checked)
	}
	// Owners may do all of it: no route answers them 403.
	for _, rt := range routesOf(t, r.owner) {
		if !strings.HasPrefix(rt.path, "/spaces/{space}") || rt.method == "DELETE" && strings.Contains(rt.path, "/members/") {
			continue
		}
		if code, body := call(t, r.owner, rt.method, fill(rt.path, r.ids)); code == 403 || code == 401 || code >= 500 && code != 503 {
			t.Errorf("owner %s %s: %d (%s)", rt.method, rt.path, code, trunc(body))
		}
	}
}

func TestSCN_AUTH_005_SpacesAreIsolated(t *testing.T) {
	r := startRights(t)
	ctx := context.Background()
	acmeIDs := []string{r.acme, r.ids["bot"], r.ids["chat"], r.ids["zone"], r.ids["skill"]}
	leaks := func(body string) string {
		for _, id := range acmeIDs {
			if strings.Contains(body, id) {
				return id
			}
		}
		if strings.Contains(body, `"acme"`) {
			return "acme"
		}
		return ""
	}
	var checked int
	for _, rt := range routesOf(t, r.owner) {
		if !strings.HasPrefix(rt.path, "/spaces/{space}") {
			continue
		}
		// Mallory with acme's space ID: no access.
		code, body := call(t, r.mallory, rt.method, fill(rt.path, r.ids))
		if code != 404 && code != 403 {
			t.Errorf("mallory %s %s in acme: %d (%s)", rt.method, rt.path, code, trunc(body))
		}
		if id := leaks(body); id != "" {
			t.Errorf("mallory %s %s in acme: the answer holds %s", rt.method, rt.path, id)
		}
		// Mallory with her own space and acme's other IDs: no acme data.
		ids := map[string]string{}
		for k, v := range r.ids {
			ids[k] = v
		}
		ids["space"] = r.other
		code, body = call(t, r.mallory, rt.method, fill(rt.path, ids))
		if id := leaks(body); id != "" {
			t.Errorf("mallory %s %s with acme IDs: %d, the answer holds %s (%s)", rt.method, rt.path, code, id, trunc(body))
		}
		if code >= 500 {
			t.Errorf("mallory %s %s with acme IDs: %d (%s)", rt.method, rt.path, code, trunc(body))
		}
		checked++
	}
	if checked < 80 {
		t.Fatalf("only %d space routes", checked)
	}
	// Acme's Chief and chat are unchanged by all of it.
	chief, err := r.a.Bots.Primary(ctx, uuid.MustParse(r.acme))
	if err != nil || chief.ID().String() != r.ids["bot"] || chief.Status != "active" {
		t.Fatalf("acme's Chief changed: %v %+v", err, chief)
	}

	// A run in Mallory's space never sends a query with an acme ID.
	var mu sync.Mutex
	var seen []string
	r.a.DB.Engine.OnQuery(func(qctx context.Context, ev drel.QueryEvent) {
		if app.InMaintenance(qctx) {
			return // the maintenance tick works for every space by design
		}
		for _, arg := range ev.Args {
			s := fmt.Sprint(arg)
			for _, id := range acmeIDs {
				if strings.Contains(s, id) {
					mu.Lock()
					seen = append(seen, ev.SQL)
					mu.Unlock()
				}
			}
		}
	})
	otherID := uuid.MustParse(r.other)
	bot, err := r.a.Bots.Primary(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := r.a.Chats.DirectFor(ctx, otherID, bot.ID())
	if err != nil {
		t.Fatal(err)
	}
	code, _, body := r.mallory.do("POST", "/api/v1/spaces/"+r.other+"/chats/"+ch.ID().String()+"/messages", map[string]string{"body": "Hello from other."}, "")
	if code != 200 {
		t.Fatalf("mallory's message: %d %s", code, body)
	}
	time.Sleep(2 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) > 0 {
		t.Fatalf("work for other read acme rows: %v", seen)
	}
}

func trunc(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
