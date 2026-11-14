package client

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Health is the reply of GET /v1/health.
type Health struct {
	Version          string `json:"version"`
	UptimeS          int64  `json:"uptime_s"`
	User             string `json:"user"`
	UID              int    `json:"uid"`
	NetworkRulesHash string `json:"network_rules_hash"`
	NetworkMode      string `json:"network_mode"`
	NetworkApplied   bool   `json:"network_applied"`
}

// ExecRequest is the body of POST /v1/exec. Set exactly one of Argv and Shell.
// Cwd is relative to the home folder. TimeoutMS of zero means the default.
type ExecRequest struct {
	Argv      []string          `json:"argv,omitempty"`
	Shell     string            `json:"shell,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMS int64             `json:"timeout_ms,omitempty"`
}

// ExecResult is the final line of an exec stream. OutputFile is relative to
// the home folder and holds the full output when Truncated is true.
type ExecResult struct {
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	Truncated  bool   `json:"truncated"`
	OutputFile string `json:"output_file"`
	DurationMS int64  `json:"duration_ms"`
}

// ExecLine is one NDJSON line of an exec stream: a chunk when Stream is set,
// else the final result.
type ExecLine struct {
	Stream string `json:"stream,omitempty"`
	Data   string `json:"data,omitempty"`
	*ExecResult
}

// Entry describes one file or folder. Path is relative to the home folder
// and uses forward slashes. Mode holds the permission bits only.
type Entry struct {
	Path    string
	Size    int64
	Mode    os.FileMode
	IsDir   bool
	ModTime time.Time
}

type entryJSON struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

// MarshalJSON writes Mode as an octal string such as "0644".
func (e Entry) MarshalJSON() ([]byte, error) {
	return json.Marshal(entryJSON{
		Path:    e.Path,
		Size:    e.Size,
		Mode:    fmt.Sprintf("%04o", uint32(e.Mode.Perm())),
		IsDir:   e.IsDir,
		ModTime: e.ModTime,
	})
}

// UnmarshalJSON reads Mode from an octal string.
func (e *Entry) UnmarshalJSON(b []byte) error {
	var j entryJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	m, err := strconv.ParseUint(j.Mode, 8, 32)
	if err != nil {
		return fmt.Errorf("entry mode %q: %w", j.Mode, err)
	}
	*e = Entry{Path: j.Path, Size: j.Size, Mode: os.FileMode(m).Perm(), IsDir: j.IsDir, ModTime: j.ModTime}
	return nil
}

// ListReply is the reply of GET /v1/files/list.
type ListReply struct {
	Entries []Entry `json:"entries"`
}

// WorkspaceRequest is the body of POST /v1/workspace/export and
// POST /v1/workspace/manifest. Path is relative to the home folder.
type WorkspaceRequest struct {
	Path    string   `json:"path"`
	Exclude []string `json:"exclude,omitempty"`
}

// Manifest is the reply of POST /v1/workspace/manifest. Files maps a path
// relative to the workspace folder to the SHA-256 hex of its content.
type Manifest struct {
	Files map[string]string `json:"files"`
}

// DiffRequest is the body of POST /v1/workspace/diff.
type DiffRequest struct {
	Path string            `json:"path"`
	Base map[string]string `json:"base"`
}

// DiffFile is one added or changed file with its full content.
type DiffFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content_b64"`
}

// Diff is the reply of POST /v1/workspace/diff. Paths are relative to the
// workspace folder.
type Diff struct {
	Added   []DiffFile `json:"added"`
	Changed []DiffFile `json:"changed"`
	Deleted []string   `json:"deleted"`
}

// ErrorReply is the body of every error response.
type ErrorReply struct {
	Error string `json:"error"`
}
