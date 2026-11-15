package app

import (
	"context"
	"errors"
	"html"
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/tools"
)

// zoneChecker runs acceptance checks on the zone computer of a bot (§9.5).
type zoneChecker struct {
	l    *computers.Lease
	home string
}

func (a *App) checker(ctx context.Context, spaceID, botID uuid.UUID) (agent.Checker, error) {
	b, err := a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	if b.ZoneID == nil {
		return nil, tools.ErrNoZone
	}
	l, err := a.Computers.Acquire(ctx, *b.ZoneID)
	if err != nil {
		return nil, err
	}
	if err := a.Computers.EnsureBotHome(ctx, l, botID); err != nil {
		l.Release()
		return nil, err
	}
	return &zoneChecker{l: l, home: computers.BotHome(botID)}, nil
}

func (z *zoneChecker) Close() { z.l.Release() }

// resolvePath keeps a check path inside the bot home or the zone's shared folder.
func (z *zoneChecker) resolvePath(p string) (string, error) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "/home/fettle/")
	clean := path.Clean("/" + p)[1:]
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", errors.New("the path is not in the bot home")
	}
	if clean == "shared" || strings.HasPrefix(clean, "shared/") {
		return clean, nil
	}
	return path.Join(z.home, clean), nil
}

func (z *zoneChecker) ReadFile(ctx context.Context, p string) ([]byte, error) {
	full, err := z.resolvePath(p)
	if err != nil {
		return nil, err
	}
	return z.l.Client.ReadFile(ctx, full)
}

func (z *zoneChecker) Run(ctx context.Context, command string) (int, string, error) {
	var out strings.Builder
	res, err := z.l.Client.Exec(ctx, client.ExecRequest{Shell: command, Cwd: z.home, TimeoutMS: 120000}, func(_, data string) {
		if out.Len() < 64<<10 {
			out.WriteString(data)
		}
	})
	if err != nil {
		return -1, "", err
	}
	return res.ExitCode, out.String(), nil
}

var (
	scriptTag = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	anyTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	wsRun     = regexp.MustCompile(`\s+`)
)

// Fetch reads a page from the zone computer, so it sees what the bot saw (its network
// policy and its hosts), and returns the page text.
func (z *zoneChecker) Fetch(ctx context.Context, url string) (string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", errors.New("only http and https sources")
	}
	var out strings.Builder
	res, err := z.l.Client.Exec(ctx, client.ExecRequest{Argv: []string{"curl", "-sSL", "--max-time", "20", "--max-filesize", "5000000", url}, Cwd: z.home, TimeoutMS: 30000}, func(stream, data string) {
		if stream == "stdout" && out.Len() < 5<<20 {
			out.WriteString(data)
		}
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", errors.New("the source did not load")
	}
	text := scriptTag.ReplaceAllString(out.String(), " ")
	text = html.UnescapeString(anyTag.ReplaceAllString(text, " "))
	return strings.TrimSpace(wsRun.ReplaceAllString(text, " ")), nil
}
