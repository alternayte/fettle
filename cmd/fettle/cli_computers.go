package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func init() {
	register("computer list", "List the zones with their computer, provider and state", runComputerList)
	register("computer add", "Turn on a computer provider and prepare it: docker, applecontainer or host", runComputerAdd)
	register("computer test", "Start a zone's computer and run one command in it (default zone: personal)", runComputerTest)
	register("computer stop", "Put a zone's computer to sleep; its workspace stays", runComputerStop)
}

type zoneRow struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ProviderKind  string   `json:"provider_kind"`
	Network       string   `json:"network"`
	Size          string   `json:"size"`
	IdleMinutes   int      `json:"idle_minutes"`
	ComputerState string   `json:"computer_state"`
	ComputerError string   `json:"computer_error"`
	Bots          []string `json:"bots"`
}

func (c *apiClient) zones(ctx context.Context) ([]zoneRow, error) {
	var out struct {
		Zones []zoneRow `json:"zones"`
	}
	err := c.do(ctx, http.MethodGet, c.sp("/zones"), nil, &out)
	return out.Zones, err
}

func (c *apiClient) zone(ctx context.Context, ref string) (zoneRow, error) {
	list, err := c.zones(ctx)
	if err != nil {
		return zoneRow{}, err
	}
	for _, z := range list {
		if z.ID == ref || strings.EqualFold(z.Name, ref) {
			return z, nil
		}
	}
	return zoneRow{}, fmt.Errorf("no zone %q in this space", ref)
}

func runComputerList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("computer list", flag.ContinueOnError)
	o := clientFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	list, err := c.zones(ctx)
	if err != nil {
		return err
	}
	w := table()
	fmt.Fprintln(w, "ZONE\tPROVIDER\tCOMPUTER\tSIZE\tNETWORK\tIDLE\tBOTS")
	for _, z := range list {
		state := dash(z.ComputerState)
		if z.ComputerError != "" {
			state += " (" + oneLine(z.ComputerError, 40) + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d min\t%d\n", z.Name, dash(z.ProviderKind), state, z.Size, z.Network, z.IdleMinutes, len(z.Bots))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	var provs struct {
		Providers []struct {
			Kind      string `json:"kind"`
			Enabled   bool   `json:"enabled"`
			Available bool   `json:"available"`
			Default   bool   `json:"default"`
		} `json:"providers"`
	}
	if err := c.do(ctx, http.MethodGet, c.sp("/computer-providers"), nil, &provs); err != nil {
		return err
	}
	fmt.Println()
	w = table()
	fmt.Fprintln(w, "PROVIDER\tON\tWORKS HERE\tDEFAULT")
	for _, p := range provs.Providers {
		fmt.Fprintf(w, "%s\t%t\t%t\t%t\n", p.Kind, p.Enabled, p.Available, p.Default)
	}
	return w.Flush()
}

type setupRow struct {
	Default   string `json:"default"`
	Providers []struct {
		Kind      string `json:"kind"`
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
		Status    struct {
			State string `json:"state"`
			Error string `json:"error"`
			Steps []struct {
				Step    string `json:"step"`
				State   string `json:"state"`
				Percent int    `json:"percent"`
				Detail  string `json:"detail"`
			} `json:"steps"`
		} `json:"status"`
	} `json:"providers"`
}

func runComputerAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("computer add", flag.ContinueOnError)
	o := clientFlags(fs)
	kilnURL := fs.String("url", "", "kiln: the URL of the Kiln host")
	kilnToken := fs.String("token", "", "kiln: the API token of the Kiln host (default: FETTLE_KILN_TOKEN)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle computer add <docker|applecontainer|host|kiln> [--url URL --token TOKEN]")
		return errUsage
	}
	kind := strings.ToLower(pos[0])
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	if kind == "host" {
		var card struct {
			ChatID string `json:"chat_id"`
		}
		if err := c.do(ctx, http.MethodPost, c.sp("/computer-providers/host/enable"), nil, &card); err != nil {
			return err
		}
		fmt.Println("The host provider runs commands on this computer itself, with your user rights.")
		fmt.Println("A card in the primary bot's chat asks you to allow it. It is on after you press Allow.")
		return nil
	}
	if kind == "e2b" {
		return fmt.Errorf("the %s provider is not in this version of Fettle", kind)
	}
	if kind == "kiln" && *kilnURL != "" {
		tok := *kilnToken
		if tok == "" {
			tok = os.Getenv("FETTLE_KILN_TOKEN")
		}
		if tok == "" {
			return errors.New("give the Kiln API token with --token or FETTLE_KILN_TOKEN")
		}
		var res struct {
			Pass       bool   `json:"pass"`
			Error      string `json:"error"`
			Configured bool   `json:"configured"`
		}
		if err := c.do(ctx, http.MethodPost, "/computer-setup/kiln-check", map[string]string{"url": *kilnURL, "token": tok}, &res); err != nil {
			return err
		}
		if !res.Pass {
			return fmt.Errorf("the Kiln host does not answer: %s", res.Error)
		}
		fmt.Println("The Kiln host answers, and the token works.")
		if !res.Configured {
			fmt.Println("Set FETTLE_KILN_URL and FETTLE_KILN_TOKEN for the server, then restart it.")
			fmt.Println("Then run fettle computer add kiln to build the computer templates.")
			return nil
		}
	}
	var s setupRow
	if err := c.do(ctx, http.MethodPost, "/computer-setup/prepare?provider="+kind, nil, &s); err != nil {
		return err
	}
	fmt.Printf("Preparing %s.\n", kind)
	last := ""
	for {
		if err := c.do(ctx, http.MethodGet, "/computer-setup", nil, &s); err != nil {
			return err
		}
		for _, p := range s.Providers {
			if p.Kind != kind {
				continue
			}
			for _, st := range p.Status.Steps {
				line := fmt.Sprintf("  %s: %s", st.Step, st.State)
				if st.Percent > 0 && st.State == "running" {
					line += fmt.Sprintf(" %d%%", st.Percent)
				}
				if st.Detail != "" {
					line += " (" + oneLine(st.Detail, 60) + ")"
				}
				if strings.Contains(last, line) {
					continue
				}
				fmt.Println(line)
			}
			last = fmt.Sprint(p.Status.Steps)
			switch p.Status.State {
			case "ready":
				fmt.Printf("%s is ready. New zones can use it; run fettle computer test to try it.\n", kind)
				return nil
			case "failed":
				return fmt.Errorf("%s could not be prepared: %s", kind, p.Status.Error)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func runComputerTest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("computer test", flag.ContinueOnError)
	o := clientFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	ref := "personal"
	if len(pos) == 1 {
		ref = pos[0]
	} else if len(pos) > 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle computer test [zone]")
		return errUsage
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	z, err := c.zone(ctx, ref)
	if err != nil {
		return err
	}
	fmt.Printf("Starting the computer of zone %s.\n", z.Name)
	var r struct {
		Provider string `json:"provider"`
		Pass     bool   `json:"pass"`
		Output   string `json:"output"`
		StartMS  int64  `json:"start_ms"`
		ExecMS   int64  `json:"exec_ms"`
		Error    string `json:"error"`
	}
	if err := c.do(ctx, http.MethodPost, c.sp("/zones/%s/computer/test", z.ID), nil, &r); err != nil {
		return err
	}
	if !r.Pass {
		if r.Error == "" {
			r.Error = "the output is " + r.Output
		}
		return fmt.Errorf("the test failed on %s: %s", dash(r.Provider), r.Error)
	}
	fmt.Printf("Pass on %s: start %.1f s, command %d ms, output %q.\n", r.Provider, float64(r.StartMS)/1000, r.ExecMS, r.Output)
	return nil
}

func runComputerStop(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("computer stop", flag.ContinueOnError)
	o := clientFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: fettle computer stop <zone>")
		return errUsage
	}
	c, err := o.connect(ctx)
	if err != nil {
		return err
	}
	z, err := c.zone(ctx, pos[0])
	if err != nil {
		return err
	}
	var after zoneRow
	if err := c.do(ctx, http.MethodPost, c.sp("/zones/%s/computer/stop", z.ID), nil, &after); err != nil {
		return err
	}
	fmt.Printf("The computer of zone %s is %s.\n", after.Name, dash(after.ComputerState))
	return nil
}
