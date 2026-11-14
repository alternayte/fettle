//go:build fakes

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

func init() {
	register("fake serve", "Run the test fakes (fake model) for end-to-end tests", runFakeServe)
}

func runFakeServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fake serve", flag.ContinueOnError)
	addr := fs.String("model-addr", "127.0.0.1:0", "listen address of the fake model")
	scripts := fs.String("scripts", "", "folder with extra YAML scripts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := fakemodel.New()
	if *scripts != "" {
		f, err = fakemodel.New(os.DirFS(filepath.Clean(*scripts)))
	}
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Printf("FAKE_MODEL_URL=http://%s/v1\n", ln.Addr())
	srv := &http.Server{Handler: f}
	go func() { _ = srv.Serve(ln) }()
	sig, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sig.Done()
	return srv.Close()
}
