package agentd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/alternayte/fettle/agentd/client"
)

const (
	execOutputCap      = 1 << 20
	execDefaultTimeout = 120 * time.Second
	execMaxTimeout     = 30 * time.Minute
	// execPipeWait bounds the wait for output after the main process exits,
	// for background children that keep the pipes open.
	execPipeWait = 2 * time.Second
)

func (s *Server) exec(w http.ResponseWriter, r *http.Request) {
	var req client.ExecRequest
	if err := decodeJSON(w, r, 1<<20, &req); err != nil {
		s.fail(w, "", err)
		return
	}
	cmd, timeout, err := s.buildCommand(req)
	if err != nil {
		s.fail(w, req.Cwd, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	out := &execSink{w: w, rc: http.NewResponseController(w), s: s}
	out.flush()
	cmd.Stdout = out.writer("stdout")
	cmd.Stderr = out.writer("stderr")
	cmd.WaitDelay = execPipeWait

	start := time.Now()
	if err := cmd.Start(); err != nil {
		code := 126
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			code = 127
		}
		out.write("stderr", []byte(cleanMessage(err)+"\n"))
		out.finish(client.ExecResult{ExitCode: code, DurationMS: time.Since(start).Milliseconds()})
		return
	}

	var timedOut atomic.Bool
	done := make(chan struct{})
	go func() {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case <-t.C:
			timedOut.Store(true)
			killGroup(cmd)
		case <-r.Context().Done():
			killGroup(cmd)
		case <-done:
		}
	}()
	werr := cmd.Wait()
	close(done)
	if werr != nil && !errors.Is(werr, exec.ErrWaitDelay) {
		var ee *exec.ExitError
		if !errors.As(werr, &ee) {
			s.logger.Warn("exec wait failed", "error", werr.Error())
		}
	}
	out.finish(client.ExecResult{
		ExitCode:   exitCode(cmd.ProcessState),
		TimedOut:   timedOut.Load(),
		DurationMS: time.Since(start).Milliseconds(),
	})
}

// buildCommand validates the request and returns the command and its timeout.
func (s *Server) buildCommand(req client.ExecRequest) (*exec.Cmd, time.Duration, error) {
	if (len(req.Argv) == 0) == (req.Shell == "") {
		return nil, 0, badRequest("set exactly one of argv and shell")
	}
	if len(req.Argv) > 0 && req.Argv[0] == "" {
		return nil, 0, badRequest("argv[0] is empty")
	}
	if req.TimeoutMS < 0 {
		return nil, 0, badRequest("timeout_ms must not be negative")
	}
	timeout := execDefaultTimeout
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	timeout = min(timeout, execMaxTimeout)

	rel, err := relPath(req.Cwd, true)
	if err != nil {
		return nil, 0, err
	}
	st, err := s.root.Stat(rel)
	if err != nil {
		return nil, 0, err
	}
	if !st.IsDir() {
		return nil, 0, badRequest("%s: cwd is not a folder", req.Cwd)
	}
	for k := range req.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return nil, 0, badRequest("env name %q is not valid", k)
		}
	}

	var cmd *exec.Cmd
	if req.Shell != "" {
		cmd = exec.Command("/bin/sh", "-c", req.Shell)
	} else {
		cmd = exec.Command(req.Argv[0], req.Argv[1:]...)
	}
	cmd.Dir = filepath.Join(s.home, rel)
	cmd.Env = s.commandEnv(req.Env)
	cmd.SysProcAttr = groupAttr()
	return cmd, timeout, nil
}

// commandEnv returns the environment of agentd without its own variables,
// with HOME set to the home folder and the request variables on top.
func (s *Server) commandEnv(extra map[string]string) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.HasPrefix(k, "FETTLE_AGENTD_") {
			continue
		}
		env[k] = v
	}
	env["HOME"] = s.home
	for k, v := range s.network.Env {
		env[k] = v
	}
	if _, ok := env["PATH"]; !ok {
		env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	for k, v := range extra {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// execSink receives stdout and stderr, streams them as NDJSON up to the
// cap, and spills the full interleaved output to a file past the cap.
type execSink struct {
	s  *Server
	w  http.ResponseWriter
	rc *http.ResponseController

	mu       sync.Mutex
	closed   bool
	broken   bool
	streamed int
	raw      bytes.Buffer
	file     *os.File
	fileRel  string
	spilled  bool
	pending  map[string][]byte
	// tail keeps the end of the last chunk, so a pattern split over two chunks still matches.
	tail    []byte
	certErr bool
}

// tlsErrorMarks are output fragments of curl, OpenSSL, Python, Node, uv and Go that show a
// certificate the computer does not trust.
var tlsErrorMarks = []string{
	"ssl certificate problem",
	"certificate verify failed",
	"unable to get local issuer certificate",
	"self-signed certificate in certificate chain",
	"self signed certificate in certificate chain",
	"unable_to_get_issuer_cert_locally",
	"self_signed_cert_in_chain",
	"unable_to_verify_leaf_signature",
	"invalid peer certificate",
	"certificate signed by unknown authority",
	"unknownissuer",
}

// noteTLS looks for a certificate error in a chunk of output.
func (o *execSink) noteTLS(p []byte) {
	if o.certErr {
		return
	}
	buf := strings.ToLower(string(append(append([]byte(nil), o.tail...), p...)))
	for _, m := range tlsErrorMarks {
		if strings.Contains(buf, m) {
			o.certErr = true
			return
		}
	}
	if len(buf) > 64 {
		buf = buf[len(buf)-64:]
	}
	o.tail = []byte(buf)
}

type streamWriter struct {
	sink   *execSink
	stream string
}

func (sw streamWriter) Write(p []byte) (int, error) {
	sw.sink.write(sw.stream, p)
	return len(p), nil
}

func (o *execSink) writer(stream string) streamWriter { return streamWriter{sink: o, stream: stream} }

func (o *execSink) write(stream string, p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || len(p) == 0 {
		return
	}
	o.noteTLS(p)
	o.record(p)
	if o.streamed >= execOutputCap {
		return
	}
	if o.pending == nil {
		o.pending = map[string][]byte{}
	}
	data := append(o.pending[stream], p...)
	cut := len(data) - incompleteTail(data)
	o.pending[stream] = append([]byte(nil), data[cut:]...)
	o.emit(stream, data[:cut])
}

// record keeps the raw output in memory up to the cap. Past the cap it
// moves the output to a file and appends there.
func (o *execSink) record(p []byte) {
	if o.file != nil {
		if _, err := o.file.Write(p); err != nil {
			o.s.logger.Warn("exec output file write failed", "error", cleanMessage(err))
		}
		return
	}
	if o.spilled {
		return
	}
	if o.raw.Len()+len(p) <= execOutputCap {
		o.raw.Write(p)
		return
	}
	o.spilled = true
	dir := filepath.Join(".fettle", "exec")
	rel := filepath.Join(dir, randomID()+".log")
	if err := o.s.root.MkdirAll(dir, 0o700); err != nil {
		o.s.logger.Warn("exec output folder failed", "error", cleanMessage(err))
		return
	}
	f, err := o.s.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		o.s.logger.Warn("exec output file failed", "error", cleanMessage(err))
		return
	}
	o.file, o.fileRel = f, fsPath(rel)
	o.record(o.raw.Bytes())
	o.raw = bytes.Buffer{}
	o.record(p)
}

// emit sends data as chunks while the stream is under the cap.
func (o *execSink) emit(stream string, data []byte) {
	if len(data) == 0 || o.broken {
		return
	}
	if room := execOutputCap - o.streamed; len(data) > room {
		cut := room
		for cut > 0 && cut > room-utf8.UTFMax && !utf8.RuneStart(data[cut]) {
			cut--
		}
		data = data[:cut]
	}
	for len(data) > 0 {
		n := min(len(data), 32<<10)
		for n < len(data) && n > 0 && !utf8.RuneStart(data[n]) {
			n--
		}
		if n == 0 {
			n = min(len(data), 32<<10)
		}
		o.line(client.ExecLine{Stream: stream, Data: string(data[:n])})
		o.streamed += n
		data = data[n:]
	}
	o.flush()
}

func (o *execSink) line(v client.ExecLine) {
	if o.broken {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if _, err := o.w.Write(b); err != nil {
		o.broken = true
	}
}

func (o *execSink) flush() {
	if !o.broken {
		if err := o.rc.Flush(); err != nil {
			o.broken = true
		}
	}
}

// finish flushes held bytes, closes the output file and writes the final line.
func (o *execSink) finish(res client.ExecResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	for _, stream := range []string{"stdout", "stderr"} {
		if o.streamed < execOutputCap {
			o.emit(stream, o.pending[stream])
		}
	}
	if o.file != nil {
		if err := o.file.Close(); err != nil {
			o.s.logger.Warn("exec output file close failed", "error", cleanMessage(err))
		}
	}
	res.Truncated = o.spilled
	res.OutputFile = o.fileRel
	if o.certErr && res.ExitCode != 0 {
		res.Hint = msgInspectsHTTPS
	}
	o.line(client.ExecLine{ExecResult: &res})
	o.flush()
}

// incompleteTail returns the length of a partial UTF-8 sequence at the end
// of b, so that a chunk never splits a character.
func incompleteTail(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax+1; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return len(b) - i
			}
			return 0
		}
	}
	return 0
}
