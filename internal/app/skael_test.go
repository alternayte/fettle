//go:build contract

package app_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// skaelImage is the Skael release this contract test pins.
const skaelImage = "ghcr.io/skael-dev/skael:0.22.0"

type skaelInstance struct {
	t   *testing.T
	URL string
	Key string
}

// startSkael runs Skael with its own PostgreSQL, claims it, signs up the owner and
// makes an API key.
func startSkael(t *testing.T) *skaelInstance {
	t.Helper()
	ctx := context.Background()
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })
	db, err := testcontainers.Run(ctx, "postgres:16-alpine",
		network.WithNetwork([]string{"skaeldb"}, nw),
		testcontainers.WithEnv(map[string]string{"POSTGRES_USER": "skael", "POSTGRES_PASSWORD": "skael", "POSTGRES_DB": "skael"}),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	testcontainers.CleanupContainer(t, db)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := testcontainers.Run(ctx, skaelImage,
		network.WithNetwork([]string{"skael"}, nw),
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithEnv(map[string]string{
			"DATABASE_URL":   "postgres://skael:skael@skaeldb:5432/skael?sslmode=disable",
			"STORAGE_PATH":   "/tmp/skills",
			"SKAEL_DATA_DIR": "/tmp/data",
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/api/setup").WithPort("8080/tcp").WithStartupTimeout(90*time.Second)),
	)
	testcontainers.CleanupContainer(t, sk)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := sk.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	s := &skaelInstance{t: t, URL: ep}
	logs, err := sk.Logs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lb, _ := io.ReadAll(logs)
	m := regexp.MustCompile(`"claim_code":"([^"]+)"`).FindSubmatch(lb)
	if m == nil {
		t.Fatalf("Skael logged no claim code:\n%s", lb)
	}
	jar, _ := cookiejar.New(nil)
	owner := &http.Client{Jar: jar}
	s.call(owner, "POST", "/api/setup/claim", "application/json", map[string]string{"code": string(m[1])}, nil)
	s.call(owner, "POST", "/api/auth/sign-up/email", "application/json", map[string]string{"email": "owner@acme.test", "name": "Acme Owner", "password": "acme-password-1"}, nil)
	var key struct {
		Key string `json:"key"`
	}
	s.call(owner, "POST", "/api/auth/keys", "application/json", map[string]string{"name": "fettle"}, &key)
	s.Key = key.Key
	return s
}

// call sends one request and fails the test on a status other than 2xx.
func (s *skaelInstance) call(c *http.Client, method, p, ctype string, body any, out any) {
	s.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, s.URL+p, r)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if s.Key != "" {
		req.Header.Set("Authorization", "Bearer "+s.Key)
	}
	resp, err := c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		s.t.Fatalf("Skael %s %s: %d %s", method, p, resp.StatusCode, rb)
	}
	if out != nil {
		if err := json.Unmarshal(rb, out); err != nil {
			s.t.Fatalf("Skael %s %s: %v", method, p, err)
		}
	}
}

// publish publishes one version of a skill as a tar.gz with SKILL.md at the root.
func (s *skaelInstance) publish(name, body string, extra map[string]string) {
	s.t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{"SKILL.md": fmt.Sprintf("---\nname: %s\ndescription: Write the Acme weekly report.\n---\n\n%s\n", name, body)}
	for k, v := range extra {
		files[k] = v
	}
	for n, c := range files {
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(c))
	}
	_ = tw.Close()
	_ = gz.Close()
	s.call(http.DefaultClient, "POST", "/api/skills/"+name+"/versions", "application/gzip", buf.Bytes(), nil)
}

func TestSCN_SKL_004_SkaelSyncAndActivations(t *testing.T) {
	sk := startSkael(t)
	sk.call(http.DefaultClient, "POST", "/api/skills", "application/json", map[string]string{"name": "acme-report", "description": "Write the Acme weekly report."}, nil)
	sk.publish("acme-report", "Version one.", nil)
	sk.call(http.DefaultClient, "POST", "/api/sets", "application/json", map[string]any{"name": "fettle-team", "entries": []map[string]string{{"name": "acme-report"}}}, nil)

	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	e.MustDo("POST", e.SP("/vault"), map[string]any{"name": "skael-key", "kind": "secret", "value": sk.Key}, nil)
	for k, v := range map[string]string{"skael.url": sk.URL, "skael.set": "fettle-team", "skael.key_item": "skael-key"} {
		e.MustDo("PUT", e.SP("/settings/%s", k), map[string]any{"value": v}, nil)
	}
	type syncOut struct{ Skills, Updated int }
	var res syncOut
	e.MustDo("POST", e.SP("/skills/sync"), nil, &res)
	if res.Skills != 1 || res.Updated != 1 {
		t.Fatalf("first sync %+v", res)
	}
	e.MustDo("POST", e.SP("/skills/sync"), nil, &res)
	if res.Updated != 0 {
		t.Fatalf("a sync without a new Skael version changed the library: %+v", res)
	}
	sk.publish("acme-report", "Version two.", map[string]string{"scripts/report.py": "print('report')\n"})
	e.MustDo("POST", e.SP("/skills/sync"), nil, &res)
	if res.Updated != 1 {
		t.Fatalf("the sync missed version two: %+v", res)
	}

	var list struct {
		Skills []SkillListItem `json:"skills"`
	}
	e.MustDo("GET", e.SP("/skills"), nil, &list)
	if len(list.Skills) != 1 || list.Skills[0].Source != "skael" || list.Skills[0].CurrentVersion != 2 {
		t.Fatalf("library %+v", list.Skills)
	}
	var detail struct {
		Versions []struct {
			Version  int      `json:"version"`
			Upstream int      `json:"upstream"`
			Files    []string `json:"files"`
		} `json:"versions"`
	}
	e.MustDo("GET", e.SP("/skills/%s", list.Skills[0].ID), nil, &detail)
	if len(detail.Versions) != 2 || detail.Versions[0].Upstream != 2 || detail.Versions[1].Upstream != 1 || len(detail.Versions[0].Files) != 2 {
		t.Fatalf("versions %+v", detail.Versions)
	}

	e.MustDo("PUT", e.SP("/skills/%s/bots/%s", list.Skills[0].ID, e.Chief().ID), map[string]any{"on": true}, nil)
	e.Fake.AddScript(fakemodel.Script{Name: "load", Match: fakemodel.Match{User: "[[skael-load]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "load_skill", Args: map[string]any{"name": "acme-report"}}}}, {Text: "Loaded."},
	}})
	run, _ := e.Post(e.Chief().ChatID, "[[skael-load]] the weekly report")
	r := e.WaitRun(run, "done")
	for _, ev := range r.Events {
		if strings.Contains(string(ev.Data), sk.Key) {
			t.Fatal("the Skael key is in the journal")
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var act struct {
			Total   int            `json:"total_count"`
			ByAgent map[string]int `json:"by_agent"`
		}
		sk.call(http.DefaultClient, "GET", "/api/skills/acme-report/activations", "", nil, &act)
		if act.Total == 1 && act.ByAgent["fettle"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Skael has no activation from Fettle: %+v", act)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// SkillListItem is the list shape this test reads.
type SkillListItem struct {
	ID             string `json:"id"`
	Source         string `json:"source"`
	CurrentVersion int    `json:"current_version"`
}
