package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/alternayte/fettle/internal/app"
)

func init() {
	register("doctor", "Check storage, keys, models, providers and settings; --network also checks the network of each zone computer", runDoctor)
}

func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfg := configFlags(fs)
	network := fs.Bool("network", false, "start the computer of each zone and check DNS, connect and TLS with curl and Chromium")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := app.New(ctx, cfg)
	if err != nil {
		fmt.Println("FAIL  start:", err)
		return err
	}
	defer a.DB.Close()
	failed := 0
	for _, c := range a.Doctor(ctx) {
		mark := "PASS"
		if !c.Pass {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("%s  %s: %s\n", mark, c.Name, c.Reason)
	}
	if *network {
		n, err := doctorNetwork(ctx, cfg.Data)
		if err != nil {
			fmt.Println("FAIL  network:", err)
			failed++
		}
		failed += n
	}
	if failed > 0 {
		return errors.New(fmt.Sprint(failed, " checks failed"))
	}
	return nil
}

// doctorNetwork runs Check network for each zone of each space through the running server,
// because only the server holds the tokens of its computers. It returns the failed zones.
func doctorNetwork(ctx context.Context, data string) (int, error) {
	o := &clientOpts{server: os.Getenv("FETTLE_SERVER"), token: os.Getenv("FETTLE_TOKEN"), data: data}
	c, err := o.connect(ctx)
	if err != nil {
		return 0, err
	}
	var me struct {
		Spaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"spaces"`
	}
	if err := c.do(ctx, http.MethodGet, "/me", nil, &me); err != nil {
		return 0, err
	}
	failed := 0
	for _, s := range me.Spaces {
		c.space = s.ID
		list, err := c.zones(ctx)
		if err != nil {
			fmt.Printf("FAIL  network of space %s: %v\n", s.Name, err)
			failed++
			continue
		}
		for _, z := range list {
			var res app.NetworkCheck
			name := "network of zone " + z.Name
			if len(me.Spaces) > 1 {
				name += " in " + s.Name
			}
			if err := c.do(ctx, http.MethodPost, c.sp("/zones/%s/computer/network-check", z.ID), nil, &res); err != nil {
				fmt.Printf("FAIL  %s: %v\n", name, err)
				failed++
				continue
			}
			mark := "PASS"
			if !res.Pass {
				mark = "FAIL"
				failed++
			}
			if res.Error != "" {
				fmt.Printf("%s  %s: the computer did not start: %s\n", mark, name, res.Error)
				continue
			}
			fmt.Printf("%s  %s: %s\n", mark, name, res.URL)
			for _, p := range res.Probes {
				var parts []string
				for _, st := range p.Steps {
					parts = append(parts, st.Name+" "+st.State)
				}
				line := fmt.Sprintf("        %s: %s", p.Tool, strings.Join(parts, ", "))
				if !p.Pass && p.Detail != "" {
					line += " (" + oneLine(p.Detail, 160) + ")"
				}
				fmt.Println(line)
			}
		}
	}
	return failed, nil
}
