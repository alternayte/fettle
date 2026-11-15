package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func bundlePkg(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.WriteHeader(&tar.Header{Name: "Fettle.app/", Mode: 0o755, Typeflag: tar.TypeDir})
	add("Fettle.app/Contents/MacOS/fettle-desktop", "binary "+version)
	add("Fettle.app/Contents/Info.plist", "<plist>"+version+"</plist>")
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUpdateChecksSignatureAndInstalls(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pkg := bundlePkg(t, "v0.3.0")
	sum := sha256.Sum256(pkg)
	hexSum := hex.EncodeToString(sum[:])
	good := Manifest{Version: "v0.3.0", Packages: map[string]Package{}}
	var srvURL string
	mux := http.NewServeMux()
	feed := good
	mux.HandleFunc("/update.json", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(feed) })
	mux.HandleFunc("/pkg", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pkg) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	good.Packages[Platform()] = Package{URL: srvURL + "/pkg", SHA256: hexSum, Signature: Sign(priv, "v0.3.0", Platform(), hexSum)}
	feed = good

	u := &Updater{Feed: srvURL + "/update.json", Current: "v0.2.0", Key: pub, HTTP: srv.Client()}
	ctx := context.Background()
	m, p, err := u.Check(ctx)
	if err != nil || m == nil || p == nil {
		t.Fatalf("check: %v %v %v", m, p, err)
	}
	if m2, _, _ := (&Updater{Feed: u.Feed, Current: "v0.3.0", Key: pub, HTTP: u.HTTP}).Check(ctx); m2 != nil {
		t.Fatal("the same version counts as an update")
	}

	// A package signed by another key is refused before the download.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	bad := *p
	bad.Signature = Sign(other, "v0.3.0", Platform(), hexSum)
	if _, err := u.Download(ctx, m, &bad, t.TempDir()); !errors.Is(err, ErrSignature) {
		t.Fatalf("a foreign signature: %v", err)
	}
	// A package whose bytes differ from the signed checksum is refused.
	wrong := *p
	wrong.SHA256 = hex.EncodeToString(make([]byte, 32))
	wrong.Signature = Sign(priv, "v0.3.0", Platform(), wrong.SHA256)
	if _, err := u.Download(ctx, m, &wrong, t.TempDir()); !errors.Is(err, ErrChecksum) {
		t.Fatalf("a wrong checksum: %v", err)
	}

	file, err := u.Download(ctx, m, p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(t.TempDir(), "Fettle.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(app, "Contents", "MacOS", "fettle-desktop"), []byte("binary v0.2.0"), 0o755)
	if err := Install(file, app); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "fettle-desktop"))
	if string(b) != "binary v0.3.0" {
		t.Fatalf("installed %q", b)
	}
	if _, err := os.Stat(app + ".old"); !os.IsNotExist(err) {
		t.Fatal("the old bundle is left behind")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.3.0", "v0.2.9", true}, {"v1.0.0", "v1.0.0-rc.1", true}, {"v1.0.0-rc.1", "v1.0.0", false}, {"v0.2.0", "v0.2.0", false}, {"v0.10.0", "v0.9.0", true}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Fatalf("Newer(%s, %s) = %t", c.a, c.b, got)
		}
	}
}
