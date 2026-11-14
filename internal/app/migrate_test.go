package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/testenv"
)

// TestMigrationsVerify applies the migrations to fresh databases and runs drel migrate verify,
// so the models and the migrations of both dialects cannot drift.
func TestMigrationsVerify(t *testing.T) {
	drelBin, err := exec.LookPath("drel")
	if err != nil {
		t.Fatal("drel is not installed; run just setup")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, d := range testenv.Dialects {
		t.Run(d, func(t *testing.T) {
			_, cfg := testenv.Open(t, d)
			dsn, conf := cfg.DB, "drel.postgres.yaml"
			if d == "sqlite" {
				dsn, conf = "file:"+cfg.SQLitePath(), "drel.yaml"
			}
			cmd := exec.Command(drelBin, "migrate", "verify", "--config", conf)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn, "GOFLAGS=-tags=drelgen")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("drel migrate verify (%s): %v\n%s", d, err, out)
			}
			if strings.Contains(string(out), "declare that the database does not hold") {
				t.Fatalf("drift:\n%s", out)
			}
		})
	}
}
