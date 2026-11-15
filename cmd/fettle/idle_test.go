//go:build !race

// The idle memory scenario measures the real binary, so it runs without the race
// detector. `just test` runs it in the overhead step.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rssMB returns the resident set size of a process in MiB.
func rssMB(t *testing.T, pid int) float64 {
	t.Helper()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("ps output %q", out)
	}
	return kb / 1024
}

func TestSCN_UX_012_IdleLocalServerStaysUnder150MB(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fettle")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	data := filepath.Join(dir, "data")
	cmd := exec.Command(bin, "serve", "--data", data, "--addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "FETTLE_NO_KEYCHAIN=1", "FETTLE_DEFAULT_PROVIDER=docker", "FETTLE_LOG_LEVEL=warn")
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() })
	go func() { _, _ = io.Copy(io.Discard, bufio.NewReader(stderr)) }()

	var base, token string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		addr, _ := os.ReadFile(filepath.Join(data, "fettle.addr"))
		tok, _ := os.ReadFile(filepath.Join(data, "local.token"))
		if len(addr) > 0 && len(tok) > 0 {
			if resp, err := http.Get("http://" + strings.TrimSpace(string(addr)) + "/healthz"); err == nil {
				_ = resp.Body.Close()
				base, token = "http://"+strings.TrimSpace(string(addr)), strings.TrimSpace(string(tok))
				break
			}
		}
	}
	if base == "" {
		t.Fatal("the server did not start")
	}
	call := func(method, path string, body string, out any) {
		t.Helper()
		req, _ := http.NewRequest(method, base+"/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
		}
		if out != nil {
			_ = json.Unmarshal(b, out)
		}
	}
	var me struct {
		Spaces []struct {
			ID string `json:"id"`
		} `json:"spaces"`
	}
	call("GET", "/me", "", &me)
	sp := me.Spaces[0].ID
	// Chief and nine more bots: 10 bots, and no computer runs.
	for i := 1; i <= 9; i++ {
		call("POST", "/spaces/"+sp+"/bots", fmt.Sprintf(`{"name":"Helper %d","job":"Waits","instructions":"Wait."}`, i), nil)
	}
	var bots struct {
		Bots []json.RawMessage `json:"bots"`
	}
	call("GET", "/spaces/"+sp+"/bots", "", &bots)
	if len(bots.Bots) != 10 {
		t.Fatalf("%d bots, want 10", len(bots.Bots))
	}

	var peak float64
	start := time.Now()
	for time.Since(start) < 10*time.Minute {
		time.Sleep(30 * time.Second)
		if m := rssMB(t, cmd.Process.Pid); m > peak {
			peak = m
		}
	}
	end := rssMB(t, cmd.Process.Pid)
	t.Logf(`{"scn":"SCN-UX-012","rss_mb":%.1f,"peak_mb":%.1f}`, end, peak)
	if end > 150 {
		t.Fatalf("RSS after 10 minutes idle is %.1f MB, want 150 MB or less (peak %.1f MB)", end, peak)
	}
}
