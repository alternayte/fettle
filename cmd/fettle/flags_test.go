package main

import (
	"context"
	"errors"
	"flag"
	"testing"
)

// Every command builds its flags without a panic; a flag that two helpers define,
// such as --token, panics only when a person runs that command.
func TestCommandsDefineEachFlagOnce(t *testing.T) {
	t.Setenv("FETTLE_DATA", t.TempDir())
	for name, c := range commands {
		if name == "fake mcp-stdio" || name == "openapi" {
			continue // they take no flags and read stdin or print the API
		}
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s panics: %v", name, r)
				}
			}()
			if err := c.run(context.Background(), []string{"-h"}); !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("%s -h: %v", name, err)
			}
		})
	}
}
