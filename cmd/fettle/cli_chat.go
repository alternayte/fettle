package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

func init() {
	register("chat", "Send a message to a bot; --watch streams the reply and asks cards as numbered prompts", runChat)
}

type cardRow struct {
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	Status  string            `json:"status"`
	Title   string            `json:"title"`
	Detail  string            `json:"detail"`
	Tool    string            `json:"tool"`
	Options []string          `json:"options"`
	Preview map[string]string `json:"preview"`
	HardAsk bool              `json:"hard_ask"`
}

type messageRow struct {
	ID         string   `json:"id"`
	ChatID     string   `json:"chat_id"`
	AuthorKind string   `json:"author_kind"`
	AuthorID   string   `json:"author_id"`
	Body       string   `json:"body"`
	Streaming  bool     `json:"streaming"`
	Card       *cardRow `json:"card"`
	EnvelopeID string   `json:"envelope_id"`
}

type choice struct {
	label, action, value string
	text                 bool // the answer is free text
}

// choices are the answers of a card as numbered prompts, in the order of the UI.
func choices(c *cardRow) []choice {
	switch c.Kind {
	case "approval":
		out := []choice{{label: "Allow once", action: "allow"}}
		if !c.HardAsk {
			out = append(out, choice{label: "Always allow", action: "always"})
		}
		return append(out, choice{label: "Deny", action: "deny"})
	case "change":
		return []choice{{label: "Allow", action: "allow"}, {label: "Deny", action: "deny"}}
	case "question":
		if len(c.Options) == 0 {
			return []choice{{label: "Type an answer", action: "answer", text: true}}
		}
		var out []choice
		for _, o := range c.Options {
			out = append(out, choice{label: o, action: "answer", value: o})
		}
		return out
	case "budget":
		return []choice{{label: "Add $1 and continue", action: "add_budget", value: "1"}, {label: "Stop", action: "stop"}}
	case "uncertain":
		return []choice{{label: "It happened", action: "done"}, {label: "It did not happen", action: "not_done"}, {label: "Stop the run", action: "stop"}}
	case "turn_limit":
		return []choice{{label: "Continue for more turns", action: "continue"}, {label: "Stop", action: "stop"}}
	case "warning":
		return []choice{{label: "Dismiss", action: "dismiss"}}
	case "verification":
		return []choice{{label: "Retry", action: "retry"}, {label: "Accept anyway", action: "accept"}, {label: "Stop", action: "stop"}}
	case "check":
		return []choice{{label: "It passes", action: "pass"}, {label: "It fails", action: "fail"}}
	case "proposal":
		return []choice{{label: "Create", action: "create"}, {label: "Decline", action: "decline"}}
	case "connect":
		return []choice{{label: "It is connected", action: "done"}, {label: "Skip", action: "skip"}}
	}
	return nil
}

// terminal reports whether a run line state ends the watch.
func terminal(state string) bool {
	return state == "done" || state == "failed" || state == "cancelled"
}

func runChat(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	o := clientFlags(fs)
	watch := fs.Bool("watch", false, "stream the reply and answer cards here")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, `Usage: fettle chat <bot> "<message>" [--watch]`)
		return errUsage
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	b, err := c.bot(ctx, pos[0])
	if err != nil {
		return err
	}
	text := strings.Join(pos[1:], " ")
	if !*watch {
		var out struct {
			RunID   string `json:"run_id"`
			Steered bool   `json:"steered"`
		}
		if err := c.do(ctx, http.MethodPost, c.sp("/chats/%s/messages", b.ChatID), map[string]any{"body": text}, &out); err != nil {
			return err
		}
		switch {
		case out.Steered:
			fmt.Printf("Sent to %s. The message steers the running work.\n", b.Name)
		case out.RunID != "":
			fmt.Printf("Sent to %s. Run %s.\n", b.Name, out.RunID)
		default:
			fmt.Printf("Sent to %s.\n", b.Name)
		}
		return nil
	}
	return watchChat(ctx, c, b, text, os.Stdin, os.Stdout)
}

// watchChat sends a message, prints the bot's reply as it streams, and asks each pending
// card as a numbered prompt on in. It ends when the run of the message ends.
func watchChat(ctx context.Context, c *apiClient, b botRow, text string, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := make(chan struct{})
	events, errc := c.stream(ctx, c.sp("/chats/%s/stream", b.ChatID), ready)
	select {
	case <-ready:
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	var sent struct {
		RunID      string `json:"run_id"`
		EnvelopeID string `json:"envelope_id"`
		Steered    bool   `json:"steered"`
	}
	if err := c.do(ctx, http.MethodPost, c.sp("/chats/%s/messages", b.ChatID), map[string]any{"body": text}, &sent); err != nil {
		return err
	}
	if sent.Steered {
		fmt.Fprintf(out, "The message steers the running work of %s.\n", b.Name)
	}
	if sent.RunID == "" && !sent.Steered {
		return errors.New("the message started no run")
	}
	lines := bufio.NewReader(in)
	asked := map[string]bool{}
	open := "" // the message whose deltas print now
	printed := map[string]bool{}
	head := func() { fmt.Fprintf(out, "%s: ", b.Name) }
	// The cards that wait already, for example from a run that this message steers.
	if err := askPending(ctx, c, b, lines, out, asked); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			return err
		case ev, ok := <-events:
			if !ok {
				return errors.New("the stream closed")
			}
			switch ev.Name {
			case "delta":
				var d struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				}
				if json.Unmarshal(ev.Data, &d) != nil {
					continue
				}
				if open != d.ID {
					if open != "" {
						fmt.Fprintln(out)
					}
					open = d.ID
					head()
				}
				printed[d.ID] = true
				fmt.Fprint(out, d.Text)
			case "message":
				var m messageRow
				if json.Unmarshal(ev.Data, &m) != nil || m.AuthorKind == "user" || m.Streaming {
					continue
				}
				if open != "" {
					fmt.Fprintln(out)
					open = ""
				}
				if m.Body != "" && !printed[m.ID] {
					if m.AuthorKind == "system" {
						fmt.Fprintf(out, "· %s\n", m.Body)
					} else {
						head()
						fmt.Fprintln(out, m.Body)
					}
					printed[m.ID] = true
				}
				if m.Card != nil && m.Card.Status == "pending" && !asked[m.Card.ID] {
					asked[m.Card.ID] = true
					if err := ask(ctx, c, m.Card, lines, out); err != nil {
						return err
					}
				}
			case "run":
				var r struct {
					RunID string  `json:"run_id"`
					State string  `json:"state"`
					Step  string  `json:"step"`
					Cost  float64 `json:"cost_usd"`
				}
				if json.Unmarshal(ev.Data, &r) != nil || r.RunID != sent.RunID {
					continue
				}
				if terminal(r.State) {
					if open != "" {
						fmt.Fprintln(out)
					}
					fmt.Fprintf(out, "· run %s, %s, $%.4f\n", r.State, dash(r.Step), r.Cost)
					if r.State == "failed" {
						return fmt.Errorf("the run failed; see fettle run show %s", r.RunID)
					}
					return nil
				}
			}
		}
	}
}

// askPending asks the cards that already wait in the chat.
func askPending(ctx context.Context, c *apiClient, b botRow, lines *bufio.Reader, out io.Writer, asked map[string]bool) error {
	var page struct {
		Messages []messageRow `json:"messages"`
	}
	if err := c.do(ctx, http.MethodGet, c.sp("/chats/%s/messages?limit=50", b.ChatID), nil, &page); err != nil {
		return err
	}
	for _, m := range page.Messages {
		if m.Card != nil && m.Card.Status == "pending" && !asked[m.Card.ID] {
			asked[m.Card.ID] = true
			if err := ask(ctx, c, m.Card, lines, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// errNoInput means stdin ended while a card waits.
var errNoInput = errors.New("a card waits for an answer and there is no input; answer it in the app")

// ask prints a card with numbered answers, reads the choice and sends it.
func ask(ctx context.Context, c *apiClient, card *cardRow, lines *bufio.Reader, out io.Writer) error {
	opts := choices(card)
	fmt.Fprintf(out, "\n[%s] %s\n", card.Kind, card.Title)
	if card.Detail != "" {
		fmt.Fprintf(out, "  %s\n", card.Detail)
	}
	keys := make([]string, 0, len(card.Preview))
	for k := range card.Preview {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(out, "  %s: %s\n", k, card.Preview[k])
	}
	if len(opts) == 0 {
		fmt.Fprintln(out, "  Answer this card in the app.")
		return nil
	}
	for i, o := range opts {
		fmt.Fprintf(out, "  %d) %s\n", i+1, o.label)
	}
	for {
		fmt.Fprintf(out, "Choose 1-%d: ", len(opts))
		line, err := lines.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" && err != nil {
			fmt.Fprintln(out)
			return errNoInput
		}
		n, perr := strconv.Atoi(line)
		if perr != nil || n < 1 || n > len(opts) {
			fmt.Fprintf(out, "Type a number from 1 to %d.\n", len(opts))
			if err != nil {
				return errNoInput
			}
			continue
		}
		o := opts[n-1]
		if o.text {
			fmt.Fprint(out, "Answer: ")
			v, err := lines.ReadString('\n')
			o.value = strings.TrimSpace(v)
			if o.value == "" {
				if err != nil {
					return errNoInput
				}
				continue
			}
		}
		body := map[string]any{"action": o.action}
		if o.value != "" {
			body["value"] = o.value
		}
		if err := c.do(ctx, http.MethodPost, c.sp("/cards/%s/answer", card.ID), body, nil); err != nil {
			return err
		}
		fmt.Fprintf(out, "· %s\n", o.label)
		return nil
	}
}
