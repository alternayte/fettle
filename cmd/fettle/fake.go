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
	"strings"
	"syscall"

	fakemcp "github.com/alternayte/fettle/internal/fakes/mcp"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	fakepush "github.com/alternayte/fettle/internal/fakes/push"
	"github.com/alternayte/fettle/internal/fakes/web"
	webtools "github.com/alternayte/fettle/internal/web"
)

func init() {
	register("fake serve", "Run the test fakes (fake model) for end-to-end tests", runFakeServe)
	register("fake mcp-stdio", "Run the fake MCP server fakemail on stdin and stdout", func(ctx context.Context, _ []string) error {
		return (&fakemcp.Mail{ShowHost: true}).ServeStdio(ctx)
	})
}

func runFakeServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fake serve", flag.ContinueOnError)
	addr := fs.String("model-addr", "127.0.0.1:0", "listen address of the fake model")
	scripts := fs.String("scripts", "", "folder with extra YAML scripts")
	webAddr := fs.String("web-addr", "", "listen address of the fixture web server (docs.test, shop.test, evil.test, slow.test, search.test)")
	pushAddr := fs.String("push-addr", "", "listen address of the push service mock")
	mcpAddr := fs.String("mcp-addr", "", "listen address of fakemail over Streamable HTTP with the key acme-mail-key-9901")
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
	if *mcpAddr != "" {
		ml, err := net.Listen("tcp", *mcpAddr)
		if err != nil {
			return err
		}
		mail := &fakemcp.Mail{HeaderKey: "acme-mail-key-9901", URL: "http://" + ml.Addr().String()}
		fmt.Printf("FAKE_MCP_URL=%s/mcp\n", mail.URL)
		ms := &http.Server{Handler: mail.Handler()}
		go func() { _ = ms.Serve(ml) }()
		defer func() { _ = ms.Close() }()
	}
	if *pushAddr != "" {
		pl, err := net.Listen("tcp", *pushAddr)
		if err != nil {
			return err
		}
		pm := fakepush.New()
		pm.URL = "http://" + pl.Addr().String()
		fmt.Printf("FAKE_PUSH_URL=%s\n", pm.URL)
		ps := &http.Server{Handler: pm}
		go func() { _ = ps.Serve(pl) }()
		defer func() { _ = ps.Close() }()
	}
	if *webAddr != "" {
		wl, err := net.Listen("tcp", *webAddr)
		if err != nil {
			return err
		}
		fmt.Printf("FAKE_WEB_ADDR=%s\n", wl.Addr())
		// The server reaches the fixture hosts through this dial override (web_fetch, web_search).
		var hosts []string
		for _, h := range []string{"docs.test", "shop.test", "evil.test", "slow.test", "search.test"} {
			hosts = append(hosts, h+"="+wl.Addr().String())
		}
		fmt.Printf("%s=%s\n", webtools.HostsEnv, strings.Join(hosts, ","))
		wh := web.New()
		wh.Log = func(line string) { fmt.Println("web " + line) }
		ws := &http.Server{Handler: wh}
		go func() { _ = ws.Serve(wl) }()
		defer func() { _ = ws.Close() }()
	}
	sig, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sig.Done()
	return srv.Close()
}
