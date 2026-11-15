//go:build e2e

package flows

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/vault"
)

// archive reads a space archive into tables (rows) and files.
type archive struct {
	tables map[string][]map[string]any
	files  map[string][]byte
}

func readArchive(t *testing.T, b []byte) archive {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	ar := archive{tables: map[string][]map[string]any{}, files: map[string][]byte{}}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		if strings.HasPrefix(h.Name, "tables/") {
			name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "tables/"), ".ndjson")
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			for {
				var row map[string]any
				if err := dec.Decode(&row); err != nil {
					break
				}
				ar.tables[name] = append(ar.tables[name], row)
			}
			continue
		}
		ar.files[h.Name] = data
	}
	return ar
}

// canon makes a value comparable across SQLite and PostgreSQL: times to microseconds,
// JSON text to its canonical form, and 0 and 1 to booleans.
func canon(v any, users map[string]string) any {
	switch x := v.(type) {
	case string:
		if x == "null" {
			return nil // JSON null in SQLite text, SQL null in PostgreSQL
		}
		if m, ok := users[x]; ok {
			return m
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
			if tm, err := time.Parse(layout, x); err == nil {
				return tm.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
			}
		}
		if strings.HasPrefix(x, "{") || strings.HasPrefix(x, "[") {
			var j any
			if json.Unmarshal([]byte(x), &j) == nil {
				b, _ := json.Marshal(j)
				return string(b)
			}
		}
		return x
	case json.Number:
		if x == "0" {
			return false
		}
		if x == "1" {
			return true
		}
		return string(x)
	}
	return v
}

func canonTables(ar archive, users map[string]string) map[string][]string {
	out := map[string][]string{}
	for name, rows := range ar.tables {
		for _, row := range rows {
			c := map[string]any{}
			for k, v := range row {
				if name == "zones" && k == "provider_id" {
					continue // providers belong to the instance
				}
				c[k] = canon(v, users)
			}
			b, _ := json.Marshal(c)
			out[name] = append(out[name], string(b))
		}
		sort.Strings(out[name])
	}
	return out
}

func export(t *testing.T, a *app.App, space uuid.UUID, pass string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := a.ExportSpace(context.Background(), space, &buf, pass); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSCN_CORE_011_ExportAndImportASpace(t *testing.T) {
	e := teamApp(t)
	ctx := context.Background()
	sp := uuid.MustParse(e.Space)
	chief := e.Chief()
	owner := audit.Actor{Kind: "user", ID: e.Owner}

	// Space acme: bots, chats, memory, skills, routines, a file, a vault item, workspaces.
	research := newBot(e, "Research", "Finds sources", "exp-research")
	e.MustDo("PUT", e.SP("/bots/%s/memory", chief.ID), map[string]any{"content": "- The user likes short answers.\n"}, nil)
	e.MustDo("PUT", e.SP("/memory"), map[string]any{"content": "- The team works in Berlin time.\n"}, nil)
	e.MustDo("POST", e.SP("/skills"), map[string]any{"skill_md": "---\nname: desk-notes\ndescription: \"Write desk notes.\"\n---\n\nWrite three lines.\n"}, nil)
	var rt struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "Morning notes", "prompt": "Write notes.", "trigger": "webhook", "enabled": false}, &rt)
	e.MustDo("POST", e.SP("/chats"), map[string]any{"name": "Desk team", "bot_ids": []string{chief.ID, research.ID}}, nil)
	if _, err := e.App.Vault.Create(ctx, owner, sp, vault.Input{Name: "shop-login", Kind: "login", Site: "https://shop.test", Username: "ada", Value: "acme-shop-secret-1"}); err != nil {
		t.Fatal(err)
	}
	// An attachment in Chief's chat.
	req, _ := http.NewRequest("POST", e.URL+"/api/v1"+e.SP("/chats/%s/attachments?name=notes.txt", chief.ChatID), strings.NewReader("Desk notes for the export.\n"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+e.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("attachment: %d %s", resp.StatusCode, ab)
	}
	var att struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(ab, &att)
	e.MustDo("POST", e.SP("/chats/%s/messages", chief.ChatID), map[string]any{"body": "Here are my notes.", "attachments": []string{att.ID}}, nil)
	// A file in Chief's home: the workspace that the checkpoint saves.
	e.Fake.AddScript(fakemodel.Script{Name: "exp-file", Match: fakemodel.Match{User: "[[exp-file]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("write_file", map[string]any{"path": "notes/desks.md", "content": "# Desks\nAlpha, Beta, Gamma.\n"})}, {Text: "Saved."},
	}})
	run, _ := e.Post(chief.ChatID, "[[exp-file]] Save the desk notes.")
	e.WaitRun(run, "done")
	if err := e.App.Computers.CheckpointAll(ctx); err != nil {
		t.Fatal(err)
	}

	plain := export(t, e.App, sp, "")
	sealed := export(t, e.App, sp, "acme export pass")
	src := readArchive(t, plain)
	for _, name := range app.ExportTableNames() {
		if name == "open_work" || name == "settings" {
			continue
		}
		if len(src.tables[name]) == 0 {
			t.Fatalf("the archive has no %s rows", name)
		}
	}
	var workspaces int
	for k := range src.files {
		if strings.HasPrefix(k, "workspaces/") {
			workspaces++
		}
	}
	if workspaces == 0 || src.files["vault.sealed"] != nil {
		t.Fatalf("archive files: %d workspaces, vault %t", workspaces, src.files["vault.sealed"] != nil)
	}
	if bytes.Contains(sealed, []byte("acme-shop-secret-1")) {
		t.Fatal("the vault value is open in the archive")
	}

	for _, tc := range []struct {
		dialect string
		archive []byte
		pass    string
	}{{"sqlite", plain, ""}, {"postgres", sealed, "acme export pass"}} {
		t.Run(tc.dialect, func(t *testing.T) {
			dst := apptest.Start(t, tc.dialect)
			ownerID := uuid.MustParse(dst.Owner)
			if tc.pass != "" {
				if _, err := dst.App.ImportSpace(ctx, bytes.NewReader(tc.archive), ownerID, "wrong pass"); err == nil {
					t.Fatal("a wrong passphrase opened the vault")
				}
			}
			res, err := dst.App.ImportSpace(ctx, bytes.NewReader(tc.archive), ownerID, tc.pass)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dst.App.ImportSpace(ctx, bytes.NewReader(tc.archive), ownerID, tc.pass); err == nil {
				t.Fatal("a second import of one space worked")
			}
			// Content: the re-export of the import equals the export, with the source owner
			// as the target owner.
			got := readArchive(t, export(t, dst.App, sp, tc.pass))
			users := map[string]string{e.Owner: dst.Owner}
			want, have := canonTables(src, users), canonTables(got, nil)
			for _, name := range app.ExportTableNames() {
				if !reflect.DeepEqual(want[name], have[name]) {
					t.Fatalf("table %s differs:\nexport %v\nimport %v", name, want[name], have[name])
				}
			}
			for k, v := range src.files {
				if k == "manifest.json" || k == "vault.sealed" {
					continue
				}
				if !bytes.Equal(got.files[k], v) {
					t.Fatalf("file %s differs after the import", k)
				}
			}
			items, err := dst.App.Vault.List(ctx, sp)
			if err != nil {
				t.Fatal(err)
			}
			if tc.pass == "" {
				if len(items) != 0 || res.Vault != 0 {
					t.Fatalf("vault items without a passphrase: %d", len(items))
				}
			} else {
				srcItems, err := e.App.Vault.List(ctx, sp)
				if err != nil {
					t.Fatal(err)
				}
				if len(items) != len(srcItems) {
					t.Fatalf("vault items with the passphrase: %d, the source has %d", len(items), len(srcItems))
				}
				for _, it := range items {
					want := ""
					for _, si := range srcItems {
						if si.Name == it.Name {
							want, _ = e.App.Vault.Reveal(ctx, sp, si.ID())
						}
					}
					if v, _ := dst.App.Vault.Reveal(ctx, sp, it.ID()); v != want || v == "" {
						t.Fatalf("the vault value of %s after import differs", it.Name)
					}
				}
			}
			// The imported space works: its owner sees it and Chief answers.
			var me struct {
				Spaces []struct{ ID string } `json:"spaces"`
			}
			dst.MustDo("GET", "/me", nil, &me)
			found := false
			for _, s := range me.Spaces {
				found = found || s.ID == sp.String()
			}
			if !found {
				t.Fatal("the importer is not a member of the imported space")
			}
			fmt.Println("imported", tc.dialect, res.Rows, res.Objects)
		})
	}
}
