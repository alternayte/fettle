package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/routines"
	"github.com/alternayte/fettle/internal/zones"
)

// hire creates the bots of an allowed proposal card (§9.8): each bot in its zone, its
// routines paused, its skills, its model roles, and one hard-ask card per grant it needs.
func (a *App) hire(ctx context.Context, user audit.Actor, spaceID uuid.UUID, list []agent.Proposal) error {
	sp, err := a.Spaces.Get(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, p := range list {
		zoneName := strings.TrimSpace(p.Zone)
		if zoneName == "" {
			zoneName = zones.Personal
		}
		z, err := a.Zones.ByName(ctx, spaceID, zoneName)
		if err != nil {
			return fmt.Errorf("%w: no zone %q", agent.ErrBadAction, zoneName)
		}
		zid := z.ID()
		b, err := a.Bots.Create(ctx, user, spaceID, bots.Input{Name: p.Name, Job: p.Job, PublicDesc: p.Job, Instructions: p.Instructions, ZoneID: &zid})
		if err != nil {
			return fmt.Errorf("%w: %w", agent.ErrBadAction, err)
		}
		if _, err := a.Chats.CreateDirect(ctx, spaceID, b.ID(), b.Name); err != nil {
			return err
		}
		for _, r := range p.Routines {
			tz := r.TZ
			if tz == "" {
				tz = sp.TimeZone
			}
			if _, _, err := a.Routines.Create(ctx, user, spaceID, routines.Input{BotID: b.ID(), Name: r.Name, Prompt: r.Prompt, Trigger: "cron", Cron: r.Cron, TZ: tz, Notify: "on_failure", Enabled: false}); err != nil {
				return fmt.Errorf("%w: routine %s: %w", agent.ErrBadAction, r.Name, err)
			}
		}
		for _, name := range p.Skills {
			if sk, err := a.Skills.ByName(ctx, spaceID, name); err == nil {
				if err := a.Skills.Assign(ctx, user, spaceID, b.ID(), sk.ID(), true); err != nil {
					return err
				}
			}
		}
		for role, model := range p.ModelRoles {
			if err := a.hireRole(ctx, user, spaceID, b.ID(), role, model); err != nil {
				return err
			}
		}
		for _, g := range p.Grants {
			if err := a.hireGrant(ctx, user, spaceID, z, g); err != nil {
				return err
			}
		}
	}
	return nil
}

// hireRole gives a new bot a model for a role: the provider of the space's role, with
// the proposed model name.
func (a *App) hireRole(ctx context.Context, user audit.Actor, spaceID, botID uuid.UUID, role, model string) error {
	if !slices.Contains(models.AllRoles, role) || strings.TrimSpace(model) == "" {
		return fmt.Errorf("%w: model role %q", agent.ErrBadAction, role)
	}
	targets, err := a.Models.Resolve(ctx, spaceID, nil, role)
	if err != nil || len(targets) == 0 {
		return fmt.Errorf("%w: the space has no model for %s", agent.ErrBadAction, role)
	}
	t := targets[0].Target
	t.Model = strings.TrimSpace(model)
	return a.Models.SetRole(ctx, user, spaceID, &botID, role, []models.Target{t})
}

// hireGrant posts the hard-ask card for one grant that a new bot needs, unless its zone
// holds it already. A name is a vault item, or a connector account as "connector/label".
func (a *App) hireGrant(ctx context.Context, user audit.Actor, spaceID uuid.UUID, z *zones.Zone, name string) error {
	kind, target, label := "", uuid.Nil, strings.TrimSpace(name)
	items, err := a.Vault.List(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if strings.EqualFold(it.Name, label) {
			kind, target = "vault_item", it.ID()
		}
	}
	if kind == "" {
		conns, err := a.Connectors.List(ctx, spaceID)
		if err != nil {
			return err
		}
		for _, c := range conns {
			accs, err := a.Connectors.Accounts(ctx, c.ID())
			if err != nil {
				return err
			}
			for _, acc := range accs {
				if strings.EqualFold(c.Name+"/"+acc.Label, label) || (len(accs) == 1 && strings.EqualFold(c.Name, label)) {
					kind, target = "connector_account", acc.ID()
				}
			}
		}
	}
	if kind == "" {
		return nil // nothing of that name exists; the user adds it later
	}
	if ok, err := a.Zones.HasGrant(ctx, z.ID(), kind, target); err != nil || ok {
		return err
	}
	_, err = a.Agent.PostChange(ctx, user, spaceID, "grant.add", "Give a zone access", "A new bot in this zone needs this access.",
		map[string]string{"zone": z.Name, "access": label}, map[string]any{"zone": z.ID(), "kind": kind, "target": target})
	return err
}
