package app

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/computers"
)

// forker runs forked sub-agents on forks of the zone computer (§9.4, §10.4).
type forker struct{ a *App }

var errNoZone = errors.New("the bot has no zone")

func (f forker) zone(ctx context.Context, spaceID, botID uuid.UUID) (uuid.UUID, error) {
	b, err := f.a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return uuid.Nil, err
	}
	if b.ZoneID == nil {
		return uuid.Nil, errNoZone
	}
	return *b.ZoneID, nil
}

// folders are the workspace folders that a fork can change: the bot home and the zone's
// shared folder, under the prefixes home/ and shared/.
func folders(botID uuid.UUID) map[string]string {
	return map[string]string{"home": computers.BotHome(botID), "shared": computers.SharedPath}
}

func manifest(ctx context.Context, c *client.Client, botID uuid.UUID) (map[string]string, error) {
	out := map[string]string{}
	for prefix, dir := range folders(botID) {
		m, err := c.Manifest(ctx, dir)
		if err != nil {
			return nil, err
		}
		for p, h := range m {
			out[prefix+"/"+p] = h
		}
	}
	return out, nil
}

func (f forker) Start(ctx context.Context, spaceID, botID, runID uuid.UUID) (map[string]string, error) {
	z, err := f.zone(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	fk, err := f.a.Computers.Fork(ctx, z, []uuid.UUID{botID})
	if err != nil {
		return nil, err
	}
	base, err := manifest(ctx, fk.Client, botID)
	if err != nil {
		_ = fk.Destroy(context.WithoutCancel(ctx))
		return nil, err
	}
	f.a.Computers.AddFork(runID, fk)
	return base, nil
}

func (f forker) Finish(ctx context.Context, botID, runID uuid.UUID, base map[string]string) ([]agent.FileDiff, error) {
	fk, ok := f.a.Computers.ForkOf(runID)
	if !ok {
		return nil, errors.New("the fork is gone")
	}
	defer func() { _ = f.a.Computers.DropFork(context.WithoutCancel(ctx), runID) }()
	var out []agent.FileDiff
	for prefix, dir := range folders(botID) {
		sub := map[string]string{}
		for p, h := range base {
			if rest, ok := strings.CutPrefix(p, prefix+"/"); ok {
				sub[rest] = h
			}
		}
		d, err := fk.Client.Diff(ctx, dir, sub)
		if err != nil {
			return nil, err
		}
		for _, x := range d.Added {
			out = append(out, agent.FileDiff{Path: prefix + "/" + x.Path, Status: "added", Content: x.Content})
		}
		for _, x := range d.Changed {
			out = append(out, agent.FileDiff{Path: prefix + "/" + x.Path, Status: "changed", Content: x.Content})
		}
		for _, p := range d.Deleted {
			out = append(out, agent.FileDiff{Path: prefix + "/" + p, Status: "deleted"})
		}
	}
	return out, nil
}

func (f forker) Current(ctx context.Context, spaceID, botID uuid.UUID) (map[string]string, error) {
	z, err := f.zone(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	l, err := f.a.Computers.Acquire(ctx, z)
	if err != nil {
		return nil, err
	}
	defer l.Release()
	return manifest(ctx, l.Client, botID)
}

func (f forker) Apply(ctx context.Context, spaceID, botID uuid.UUID, d agent.FileDiff) error {
	z, err := f.zone(ctx, spaceID, botID)
	if err != nil {
		return err
	}
	prefix, rest, ok := strings.Cut(d.Path, "/")
	dir, known := folders(botID)[prefix]
	if !ok || !known {
		return errors.New("a diff path is outside the bot home and shared/")
	}
	full := path.Join(dir, path.Clean("/" + rest)[1:])
	l, err := f.a.Computers.Acquire(ctx, z)
	if err != nil {
		return err
	}
	defer l.Release()
	if d.Status == "deleted" {
		return l.Client.DeleteFile(ctx, full, false)
	}
	return l.Client.WriteFile(ctx, full, d.Content, 0o644)
}

func (f forker) Drop(ctx context.Context, runID uuid.UUID) {
	_ = f.a.Computers.DropFork(context.WithoutCancel(ctx), runID)
}
