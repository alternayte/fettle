package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/alternayte/fettle/internal/app"
)

func init() {
	register("openapi", "Print the OpenAPI document of the HTTP API", func(context.Context, []string) error {
		b, err := app.OpenAPI("1.0")
		if err != nil {
			return err
		}
		var out bytes.Buffer
		if err := json.Indent(&out, b, "", "  "); err != nil {
			return err
		}
		fmt.Println(out.String())
		return nil
	})
}
