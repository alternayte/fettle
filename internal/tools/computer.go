// Package tools holds the built-in tools that act on a zone's computer (Appendix E).
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/envelopes"
)

// OffloadLimit is the tool output size above which the output goes to a file in the bot home (§8.1).
const OffloadLimit = 8 * 1024

// HeadTail is the size of the head and the tail kept in context for offloaded output.
const HeadTail = 1024

// Computer gives tools a lease on the computer of the bot's zone.
type Computer struct {
	Bots      *bots.Service
	Computers *computers.Manager
}

// ErrNoZone means the bot has no zone, so it has no computer.
var ErrNoZone = errors.New("this bot has no zone, so it has no computer")

// lease acquires the computer of the bot's zone and prepares the bot home.
func (c *Computer) lease(ctx context.Context, env *agent.ToolEnv) (*computers.Lease, string, error) {
	if env.Fork {
		l, ok := c.Computers.ForkLease(env.RunID)
		if !ok {
			return nil, "", errors.New("the fork of this helper is gone; report what you have")
		}
		return l, computers.BotHome(env.BotID), nil
	}
	b, err := c.Bots.Get(ctx, env.SpaceID, env.BotID)
	if err != nil {
		return nil, "", err
	}
	if b.ZoneID == nil {
		return nil, "", ErrNoZone
	}
	l, err := c.Computers.Acquire(ctx, *b.ZoneID)
	if err != nil {
		return nil, "", err
	}
	if err := c.Computers.EnsureBotHome(ctx, l, env.BotID); err != nil {
		l.Release()
		return nil, "", err
	}
	return l, computers.BotHome(env.BotID), nil
}

// resolve maps a path from the model to a path under home. Paths may be relative to the
// bot home, or start with shared/ for the zone's shared folder. Other homes are refused (SI-04).
// Resolve maps a path from the model to a path in the computer: relative to the bot home,
// or under shared/. Other homes are refused (SI-04).
func Resolve(home, p string) (string, error) { return resolve(home, p) }

func resolve(home, p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/home/fettle/")
	p = strings.TrimPrefix(p, "~/")
	if p == "" || p == "." {
		return home, nil
	}
	clean := path.Clean("/" + p)[1:]
	switch {
	case clean == "shared" || strings.HasPrefix(clean, "shared/"):
		return clean, nil
	case strings.HasPrefix(clean, "bots/"):
		if clean == home || strings.HasPrefix(clean, home+"/") {
			return clean, nil
		}
		return "", errors.New("this path is in the home of another bot")
	case strings.HasPrefix(p, "/"):
		return "", errors.New("use a path in your home or in shared/")
	}
	return path.Join(home, clean), nil
}

func display(home, p string) string {
	if p == home {
		return "."
	}
	return strings.TrimPrefix(p, home+"/")
}

// HeadAndTail shortens text to its first and last kb bytes.
func HeadAndTail(s string, kb int) string {
	if len(s) <= 2*kb {
		return s
	}
	return s[:kb] + "\n…\n" + s[len(s)-kb:]
}

// offload stores a large output in the bot home and returns the context text (REQ-AGT-002).
func offload(ctx context.Context, cl *client.Client, home string, env *agent.ToolEnv, text string) string {
	if len(text) <= OffloadLimit {
		return text
	}
	name := fmt.Sprintf(".fettle/outputs/%d.txt", env.Seq)
	if err := cl.WriteFile(ctx, path.Join(home, name), []byte(text), 0o644); err != nil {
		return HeadAndTail(text, HeadTail) + "\n(The full output could not be saved: " + err.Error() + ")"
	}
	return fmt.Sprintf("The output has %d bytes. The full output is in %s. First and last 1 KB:\n%s", len(text), name, HeadAndTail(text, HeadTail))
}

func schema(s string) json.RawMessage { return json.RawMessage(strings.TrimSpace(s)) }

func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(args, &v); err != nil {
		return v, fmt.Errorf("the tool arguments do not match the schema: %w", err)
	}
	return v, nil
}

// Register adds exec and the file tools.
func (c *Computer) Register(r *agent.Registry) {
	r.Add(agent.Tool{
		Name:          "exec",
		Description:   "Run a shell command in your computer. The working folder is your home unless you give cwd. Output over 8 KB goes to a file.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"command":{"type":"string"},"cwd":{"type":"string"},"timeout_s":{"type":"integer","minimum":1,"maximum":1800}},"required":["command"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct {
				Command  string `json:"command"`
				Cwd      string `json:"cwd"`
				TimeoutS int    `json:"timeout_s"`
			}](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			cwd, err := resolve(home, in.Cwd)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if in.TimeoutS == 0 {
				in.TimeoutS = 120
			}
			var out strings.Builder
			res, err := l.Client.Exec(ctx, client.ExecRequest{Shell: in.Command, Cwd: cwd, TimeoutMS: int64(in.TimeoutS) * 1000}, func(_, data string) { out.WriteString(data) })
			if err != nil {
				return agent.ToolOutput{}, err
			}
			text := out.String()
			status := fmt.Sprintf("exit code %d", res.ExitCode)
			if res.TimedOut {
				status = fmt.Sprintf("timed out after %d s", in.TimeoutS)
			}
			if res.Truncated && res.OutputFile != "" {
				status += ". The output was over 1 MiB; the rest is in " + display(home, res.OutputFile)
			}
			return agent.ToolOutput{Text: status + "\n" + offload(ctx, l.Client, home, env, text)}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "read_file",
		Description:   "Read a text file from your home or from shared/.",
		NeedsComputer: true,
		Class:         envelopes.ClassRead,
		Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct{ Path string }](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			p, err := resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			b, err := l.Client.ReadFile(ctx, p)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: offload(ctx, l.Client, home, env, string(b))}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "list_files",
		Description:   "List the files of a folder in your home or in shared/.",
		NeedsComputer: true,
		Class:         envelopes.ClassRead,
		Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"},"recursive":{"type":"boolean"}},"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct {
				Path      string `json:"path"`
				Recursive bool   `json:"recursive"`
			}](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			p, err := resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			ents, err := l.Client.List(ctx, p, in.Recursive)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			var b strings.Builder
			for i, e := range ents {
				if i >= 500 {
					fmt.Fprintf(&b, "… %d more\n", len(ents)-i)
					break
				}
				kind := fmt.Sprintf("%d bytes", e.Size)
				if e.IsDir {
					kind = "folder"
				}
				fmt.Fprintf(&b, "%s (%s)\n", display(home, e.Path), kind)
			}
			if b.Len() == 0 {
				b.WriteString("The folder is empty.")
			}
			return agent.ToolOutput{Text: b.String()}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "search_files",
		Description:   "Search text in the files of your home or of shared/ with ripgrep.",
		NeedsComputer: true,
		Class:         envelopes.ClassRead,
		Schema:        schema(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct{ Pattern, Path string }](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			p, err := resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			var out strings.Builder
			_, err = l.Client.Exec(ctx, client.ExecRequest{Argv: []string{"rg", "--line-number", "--max-count", "50", "--", in.Pattern, "."}, Cwd: p, TimeoutMS: 60000}, func(stream, data string) {
				if stream == "stdout" {
					out.WriteString(data)
				}
			})
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if out.Len() == 0 {
				return agent.ToolOutput{Text: "No matches."}, nil
			}
			return agent.ToolOutput{Text: offload(ctx, l.Client, home, env, out.String())}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "write_file",
		Description:   "Write a text file in your home or in shared/. It replaces the file.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct{ Path, Content string }](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			p, err := resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if err := l.Client.WriteFile(ctx, p, []byte(in.Content), 0o644); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: fmt.Sprintf("Wrote %d bytes to %s.", len(in.Content), display(home, p))}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "edit_file",
		Description:   "Replace one exact text in a file with new text. The old text must occur once.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			p, err := resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			b, err := l.Client.ReadFile(ctx, p)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			n := strings.Count(string(b), in.OldText)
			if in.OldText == "" || n != 1 {
				return agent.ToolOutput{}, fmt.Errorf("the old text occurs %d times; it must occur once", n)
			}
			if err := l.Client.WriteFile(ctx, p, []byte(strings.Replace(string(b), in.OldText, in.NewText, 1)), 0o644); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: "Changed " + display(home, p) + "."}, nil
		},
	})
	r.Add(agent.Tool{
		Name:          "delete_file",
		Description:   "Delete a file or an empty folder in your home or in shared/.",
		NeedsComputer: true,
		Class:         envelopes.ClassWriteLocal,
		Schema:        schema(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			in, err := decode[struct{ Path string }](args)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, home, err := c.lease(ctx, env)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			defer l.Release()
			p, err := resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if p == home {
				return agent.ToolOutput{}, errors.New("you cannot delete your home")
			}
			if err := l.Client.DeleteFile(ctx, p, false); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: "Deleted " + display(home, p) + "."}, nil
		},
	})
}
