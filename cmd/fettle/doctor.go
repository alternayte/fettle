package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/alternayte/fettle/internal/app"
)

func init() {
	register("doctor", "Check storage, keys, models, providers and settings", runDoctor)
}

func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfg := configFlags(fs)
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
	if failed > 0 {
		return errors.New(fmt.Sprint(failed, " checks failed"))
	}
	return nil
}
