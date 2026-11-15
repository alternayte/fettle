package app_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
)

// importArchive posts an archive to the import route of e as a multipart form.
func importArchive(t *testing.T, e *apptest.Env, archive []byte, pass string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if pass != "" {
		_ = mw.WriteField("passphrase", pass)
	}
	fw, _ := mw.CreateFormFile("file", "acme.fettle.tar.gz")
	_, _ = fw.Write(archive)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.URL+app.APIPrefix+"/space-import", &body)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestSpaceExportAndImportOverHTTP(t *testing.T) {
	src := apptest.Start(t, "sqlite")
	src.MustDo("POST", src.SP("/bots"), map[string]string{"name": "Ledger", "job": "Checks invoices"}, nil)

	req, _ := http.NewRequest("POST", src.URL+app.APIPrefix+src.SP("/export"), strings.NewReader(`{"passphrase":"acme export passphrase"}`))
	req.Header.Set("Authorization", "Bearer "+src.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(archive) < 2 || archive[0] != 0x1f || archive[1] != 0x8b ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), ".fettle.tar.gz") {
		t.Fatalf("export: %d %q, %d bytes", resp.StatusCode, resp.Header.Get("Content-Disposition"), len(archive))
	}

	// The same instance already has the space.
	if code, body := importArchive(t, src, archive, "acme export passphrase"); code != 409 {
		t.Fatalf("an import into the source instance: %d %s", code, body)
	}

	dst := apptest.Start(t, "sqlite")
	if code, body := importArchive(t, dst, archive, "a wrong passphrase"); code != 422 || !strings.Contains(body, "passphrase") {
		t.Fatalf("a wrong passphrase: %d %s", code, body)
	}
	if code, body := importArchive(t, dst, archive, "acme export passphrase"); code != 201 || !strings.Contains(body, `"space_id"`) {
		t.Fatalf("import: %d %s", code, body)
	}
	var me struct {
		Spaces []struct{ ID, Name string } `json:"spaces"`
	}
	dst.MustDo("GET", "/me", nil, &me)
	if len(me.Spaces) != 2 {
		t.Fatalf("after the import: %+v", me.Spaces)
	}
	found := false
	for _, s := range me.Spaces {
		if s.ID != dst.Space {
			var bots struct {
				Bots []struct{ Name string } `json:"bots"`
			}
			dst.MustDo("GET", "/spaces/"+s.ID+"/bots", nil, &bots)
			for _, b := range bots.Bots {
				found = found || b.Name == "Ledger"
			}
		}
	}
	if !found {
		t.Fatal("the imported space has no Ledger")
	}
}
