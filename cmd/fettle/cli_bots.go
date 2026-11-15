package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

func init() {
	register("bot list", "List the bots of a space (--archived)", runBotList)
	register("bot show", "Show a bot: profile, zone, skills and instructions", runBotShow)
	register("bot create", "Create a bot (--name, --job, --instructions, --zone) or one from a template (--template)", runBotCreate)
	register("bot archive", "Archive a bot. Its routines pause; its chat stays.", runBotArchive)
	register("run show", "Show a run: its line and its journal", runRunShow)
}

func table() *tabwriter.Writer { return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0) }

func runBotList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot list", flag.ContinueOnError)
	o := clientFlags(fs)
	archived := fs.Bool("archived", false, "also list archived bots")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	list, err := c.bots(ctx)
	if err != nil {
		return err
	}
	w := table()
	fmt.Fprintln(w, "NAME\tJOB\tZONE\tSTATUS\tNOW")
	for _, b := range list {
		if b.Status != "active" && !*archived {
			continue
		}
		name := b.Name
		if b.IsPrimary {
			name += " (primary)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, b.Job, dash(b.ZoneName), b.Status, dash(b.Activity))
	}
	return w.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func runBotShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot show", flag.ContinueOnError)
	o := clientFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle bot show <bot>")
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
	var full struct {
		Instructions    string `json:"instructions"`
		InstructionsRev int    `json:"instructions_rev"`
	}
	if err := c.do(ctx, http.MethodGet, c.sp("/bots/%s", b.ID), nil, &full); err != nil {
		return err
	}
	var lib struct {
		Skills []struct {
			Name string   `json:"name"`
			Bots []string `json:"bots"`
		} `json:"skills"`
	}
	if err := c.do(ctx, http.MethodGet, c.sp("/skills"), nil, &lib); err != nil {
		return err
	}
	var skills []string
	for _, s := range lib.Skills {
		for _, id := range s.Bots {
			if id == b.ID {
				skills = append(skills, s.Name)
			}
		}
	}
	w := table()
	fmt.Fprintf(w, "Name\t%s\n", b.Name)
	fmt.Fprintf(w, "ID\t%s\n", b.ID)
	fmt.Fprintf(w, "Job\t%s\n", b.Job)
	fmt.Fprintf(w, "Primary\t%t\n", b.IsPrimary)
	fmt.Fprintf(w, "Status\t%s\n", b.Status)
	fmt.Fprintf(w, "Zone\t%s\n", dash(b.ZoneName))
	fmt.Fprintf(w, "Skills\t%s\n", dash(strings.Join(skills, ", ")))
	fmt.Fprintf(w, "Chat\t%s\n", b.ChatID)
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nInstructions (version %d):\n%s\n", full.InstructionsRev, full.Instructions)
	return nil
}

func runBotCreate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot create", flag.ContinueOnError)
	o := clientFlags(fs)
	name := fs.String("name", "", "bot name")
	job := fs.String("job", "", "one line about the bot's job")
	instr := fs.String("instructions", "", "instructions, or @file to read them from a file")
	zone := fs.String("zone", "", "zone name (default: personal)")
	tmpl := fs.String("template", "", "create from a template: chief-of-staff, inbox, research, weekly-report or pr-reviewer")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	var b botRow
	if *tmpl != "" {
		if err := c.do(ctx, http.MethodPost, c.sp("/templates/%s", *tmpl), nil, &b); err != nil {
			return err
		}
		fmt.Printf("Created %s from the template %s, with its skill and its routine paused.\n", b.Name, *tmpl)
		return nil
	}
	if *name == "" || *job == "" {
		fmt.Fprintln(os.Stderr, "Usage: fettle bot create --name <name> --job <job> [--instructions <text|@file>] [--zone <zone>]")
		return errUsage
	}
	text := *instr
	if strings.HasPrefix(text, "@") {
		raw, err := os.ReadFile(strings.TrimPrefix(text, "@"))
		if err != nil {
			return err
		}
		text = string(raw)
	}
	body := map[string]any{"name": *name, "job": *job, "public_desc": *job, "instructions": text}
	if *zone != "" {
		var zs struct {
			Zones []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"zones"`
		}
		if err := c.do(ctx, http.MethodGet, c.sp("/zones"), nil, &zs); err != nil {
			return err
		}
		for _, z := range zs.Zones {
			if strings.EqualFold(z.Name, *zone) || z.ID == *zone {
				body["zone_id"] = z.ID
			}
		}
		if body["zone_id"] == nil {
			return fmt.Errorf("no zone %q in this space", *zone)
		}
	}
	if err := c.do(ctx, http.MethodPost, c.sp("/bots"), body, &b); err != nil {
		return err
	}
	fmt.Printf("Created %s (%s) in zone %s.\n", b.Name, b.ID, dash(b.ZoneName))
	return nil
}

func runBotArchive(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bot archive", flag.ContinueOnError)
	o := clientFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle bot archive <bot>")
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
	if err := c.do(ctx, http.MethodPost, c.sp("/bots/%s/archive", b.ID), nil, nil); err != nil {
		return err
	}
	fmt.Printf("Archived %s. Its routines are paused. Restore it from its bot page.\n", b.Name)
	return nil
}

type runView struct {
	Goal    string `json:"goal"`
	BotName string `json:"bot_name"`
	Line    struct {
		State      string  `json:"state"`
		Step       string  `json:"step"`
		CostUSD    float64 `json:"cost_usd"`
		Tokens     int64   `json:"tokens"`
		DurationMS int64   `json:"duration_ms"`
		Helpers    int     `json:"helpers"`
		Verified   *bool   `json:"verified"`
	} `json:"line"`
	Events []struct {
		Seq  int64           `json:"seq"`
		Type string          `json:"type"`
		At   time.Time       `json:"at"`
		Data json.RawMessage `json:"data"`
	} `json:"events"`
}

func runRunShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run show", flag.ContinueOnError)
	o := clientFlags(fs)
	full := fs.Bool("full", false, "print each event's data in full")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle run show <run-id> [--full]")
		return errUsage
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	var r runView
	if err := c.do(ctx, http.MethodGet, c.sp("/runs/%s", pos[0]), nil, &r); err != nil {
		return err
	}
	verified := "-"
	if r.Line.Verified != nil {
		verified = fmt.Sprint(*r.Line.Verified)
	}
	w := table()
	fmt.Fprintf(w, "Bot\t%s\n", r.BotName)
	fmt.Fprintf(w, "Goal\t%s\n", oneLine(r.Goal, 100))
	fmt.Fprintf(w, "State\t%s\n", r.Line.State)
	fmt.Fprintf(w, "Step\t%s\n", dash(r.Line.Step))
	fmt.Fprintf(w, "Helpers\t%d\n", r.Line.Helpers)
	fmt.Fprintf(w, "Verified\t%s\n", verified)
	fmt.Fprintf(w, "Cost\t$%.4f, %d tokens, %.1f s\n", r.Line.CostUSD, r.Line.Tokens, float64(r.Line.DurationMS)/1000)
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Println()
	w = table()
	fmt.Fprintln(w, "SEQ\tTIME\tEVENT\tDATA")
	for _, ev := range r.Events {
		data := string(ev.Data)
		if !*full {
			data = oneLine(data, 90)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", ev.Seq, ev.At.Local().Format("15:04:05"), ev.Type, data)
	}
	return w.Flush()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
