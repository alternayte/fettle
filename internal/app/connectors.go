package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/connectors"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/tools"
)

func init() {
	settings.Register(settings.Def{Key: "connectors.composio_url", Scope: "instance", Default: "https://mcp.composio.dev/mcp", Doc: "The MCP endpoint that a Composio key connects to."})
}

// toolCache keeps the tool lists of connector accounts for a minute, so a model turn does
// not list tools at every connector.
type toolCache struct {
	mu   sync.Mutex
	m    map[string]cachedTools
	ttl  time.Duration
	nowf func() time.Time
}

type cachedTools struct {
	at    time.Time
	tools []connectors.ToolInfo
}

func (c *toolCache) get(key string) ([]connectors.ToolInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || c.nowf().Sub(e.at) > c.ttl {
		return nil, false
	}
	return e.tools, true
}

func (c *toolCache) put(key string, t []connectors.ToolInfo) {
	c.mu.Lock()
	c.m[key] = cachedTools{at: c.nowf(), tools: t}
	c.mu.Unlock()
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if s == "" {
		s = "x"
	}
	return s
}

// setupConnectors connects bots to the accounts that their zone holds (REQ-CON-003).
func (a *App) setupConnectors() {
	a.Connectors = connectors.New(a.DB.Engine, a.Audit, a.Vault, a.HTTP)
	a.Connectors.CallbackURL = func() string { return a.URL() + APIPrefix + "/oauth/callback" }
	a.Connectors.Stdio = a.stdioConnector
	cache := &toolCache{m: map[string]cachedTools{}, ttl: time.Minute, nowf: time.Now}
	a.Agent.Dynamic = func(ctx context.Context, spaceID, botID uuid.UUID) ([]agent.Tool, error) {
		b, err := a.Bots.Get(ctx, spaceID, botID)
		if err != nil || b.ZoneID == nil {
			return nil, err
		}
		grants, err := a.Zones.Grants(ctx, *b.ZoneID)
		if err != nil {
			return nil, err
		}
		type pair struct {
			c   *connectors.Connector
			acc *connectors.ConnectorAccount
		}
		var pairs []pair
		perConn := map[uuid.UUID]int{}
		for _, g := range grants {
			if g.Kind != "connector_account" {
				continue
			}
			acc, err := a.Connectors.Account(ctx, spaceID, g.TargetID)
			if err != nil {
				continue
			}
			c, err := a.Connectors.Get(ctx, spaceID, acc.ConnectorID)
			if err != nil {
				continue
			}
			pairs = append(pairs, pair{c, acc})
			perConn[c.ID()]++
		}
		var out []agent.Tool
		var errs []string
		for _, p := range pairs {
			settings, _ := json.Marshal(p.c.Tools)
			key := p.acc.ID().String() + "@" + string(settings)
			infos, ok := cache.get(key)
			if !ok {
				infos, err = a.Connectors.Tools(ctx, p.c, p.acc, botID)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", p.c.Name, err))
					continue
				}
				cache.put(key, infos)
			}
			prefix := slug(p.c.Name)
			if perConn[p.c.ID()] > 1 {
				prefix += "_" + slug(p.acc.Label)
			}
			for _, ti := range infos {
				if ti.Disabled {
					continue
				}
				out = append(out, a.connectorTool(p.c, p.acc, prefix, ti))
			}
		}
		if len(errs) > 0 {
			return out, fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return out, nil
	}
}

func (a *App) connectorTool(c *connectors.Connector, acc *connectors.ConnectorAccount, prefix string, ti connectors.ToolInfo) agent.Tool {
	name := prefix + "__" + slug(ti.Name)
	if len(name) > 64 {
		name = name[:64]
	}
	schema := ti.Schema
	if len(schema) == 0 || string(schema) == "null" {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	connID, accID, tool := c.ID(), acc.ID(), ti.Name
	return agent.Tool{
		Name: name, Description: fmt.Sprintf("%s (%s, account %s): %s", ti.Name, c.Name, acc.Label, ti.Description), Class: ti.Class, Schema: schema, Connector: c.Name,
		Target: func(args json.RawMessage) (string, string) {
			var in map[string]any
			_ = json.Unmarshal(args, &in)
			to, _ := in["to"].(string)
			return c.URL, to
		},
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			c, err := a.Connectors.Get(ctx, env.SpaceID, connID)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			acc, err := a.Connectors.Account(ctx, env.SpaceID, accID)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			text, err := a.Connectors.Call(ctx, c, acc, env.BotID, tool, args)
			return agent.ToolOutput{Text: text, Origin: agent.OriginConnector}, err
		},
	}
}

// leasedConn releases the computer lease when the stdio stream closes.
type leasedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *leasedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// stdioConnector runs a stdio MCP server in the computer of a bot's zone, in its home.
func (a *App) stdioConnector(ctx context.Context, spaceID, botID uuid.UUID, command []string) (io.ReadWriteCloser, error) {
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
	conn, err := l.Client.MCPStdio(ctx, command, computers.BotHome(botID))
	if err != nil {
		l.Release()
		return nil, err
	}
	return &leasedConn{Conn: conn, release: l.Release}, nil
}
