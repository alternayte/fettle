package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/zones"
)

// ZoneView is a zone with its provider and its computer state.
type ZoneView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ProviderID    string   `json:"provider_id,omitempty"`
	ProviderKind  string   `json:"provider_kind"`
	Network       string   `json:"network"`
	AllowHosts    []string `json:"allow_hosts"`
	Size          string   `json:"size"`
	IdleMinutes   int      `json:"idle_minutes"`
	ShareBlocked  bool     `json:"share_blocked" doc:"Handoffs from this zone copy no files to other zones."`
	ComputerState string   `json:"computer_state"`
	ComputerError string   `json:"computer_error,omitempty"`
	Bots          []string `json:"bots"`
}

// ComputerProviderView is a computer provider of the instance.
type ComputerProviderView struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Enabled   bool           `json:"enabled"`
	Available bool           `json:"available" doc:"The provider works on this server."`
	Default   bool           `json:"default"`
	Caps      computers.Caps `json:"caps"`
}

type zoneBody struct {
	Name        string   `json:"name" minLength:"1" maxLength:"60"`
	ProviderID  string   `json:"provider_id,omitempty" doc:"Empty uses the default provider."`
	Network     string   `json:"network" enum:"open,allowlist"`
	AllowHosts  []string `json:"allow_hosts,omitempty" maxItems:"100"`
	Size        string   `json:"size" enum:"small,browser,desktop"`
	IdleMinutes int      `json:"idle_minutes,omitempty" minimum:"1" maximum:"1440"`
	// ShareBlocked stops handoffs from copying this zone's files out (§9.7).
	ShareBlocked bool `json:"share_blocked,omitempty"`
}

func (b zoneBody) input() (zones.Input, error) {
	in := zones.Input{Name: b.Name, Network: b.Network, Size: b.Size, IdleMinutes: b.IdleMinutes, ShareBlocked: b.ShareBlocked}
	for _, h := range b.AllowHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			in.AllowHosts = append(in.AllowHosts, h)
		}
	}
	if b.ProviderID != "" {
		id, err := uuid.Parse(b.ProviderID)
		if err != nil {
			return in, huma.Error422UnprocessableEntity("provider_id is not valid")
		}
		in.ProviderID = &id
	}
	return in, nil
}

// CardReply is a hard-ask card that a change waits on.
type CardReply struct {
	CardID    string `json:"card_id"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
}

type ZonePath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Zone  uuid.UUID `path:"zone"`
}

func (a *App) zoneView(ctx context.Context, z *zones.Zone) ZoneView {
	v := ZoneView{ID: z.ID().String(), Name: z.Name, Network: z.Network, AllowHosts: z.AllowHosts, Size: z.Size, IdleMinutes: z.IdleMinutes, ShareBlocked: z.ShareBlocked, ComputerState: computers.StateNone, Bots: []string{}}
	if v.AllowHosts == nil {
		v.AllowHosts = []string{}
	}
	if row, err := a.Computers.ProviderOf(ctx, z); err == nil {
		v.ProviderKind = row.Kind
		if z.ProviderID != nil {
			v.ProviderID = z.ProviderID.String()
		}
	}
	if c, err := a.Computers.Current(ctx, z.ID()); err == nil && c != nil {
		v.ComputerState, v.ComputerError = c.State, c.Error
	}
	if bs, err := a.Bots.List(ctx, z.SpaceID, false); err == nil {
		for _, b := range bs {
			if b.ZoneID != nil && *b.ZoneID == z.ID() {
				v.Bots = append(v.Bots, b.Name)
			}
		}
	}
	return v
}

// registerChanges adds the hard-ask changes that the zone, provider and policy routes post
// (§10.1, §11.2). They apply when a person allows the card.
func (a *App) registerChanges() {
	a.Agent.RegisterChange("zone.create", func(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, raw json.RawMessage) error {
		var in zones.Input
		if err := json.Unmarshal(raw, &in); err != nil {
			return err
		}
		_, err := a.Zones.Create(ctx, actor, spaceID, in)
		return err
	})
	a.Agent.RegisterChange("grant.add", func(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, raw json.RawMessage) error {
		var g struct {
			Zone   uuid.UUID `json:"zone"`
			Kind   string    `json:"kind"`
			Target uuid.UUID `json:"target"`
		}
		if err := json.Unmarshal(raw, &g); err != nil {
			return err
		}
		return a.Zones.AddGrant(ctx, actor, spaceID, g.Zone, g.Kind, g.Target)
	})
	a.Agent.RegisterChange("provider.enable_host", func(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, _ json.RawMessage) error {
		return a.Computers.SetEnabled(ctx, actor, &spaceID, "host", true)
	})

	a.Agent.RegisterChange("approval.mode", func(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, raw json.RawMessage) error {
		var mode string
		if err := json.Unmarshal(raw, &mode); err != nil {
			return err
		}
		_, err := a.Settings.Set(ctx, actor, &spaceID, "approval.mode", mode)
		return err
	})
}

// registerZones adds the zone and computer provider routes.
func (a *App) registerZones(api huma.API) {
	type zonesOut struct {
		Body struct {
			Zones []ZoneView `json:"zones"`
		}
	}
	huma.Register(api, op("list-zones", http.MethodGet, "/spaces/{space}/zones", "List zones with their computers", "computers"), func(ctx context.Context, in *SpacePath) (*zonesOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		zs, err := a.Zones.List(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &zonesOut{}
		out.Body.Zones = []ZoneView{}
		for _, z := range zs {
			out.Body.Zones = append(out.Body.Zones, a.zoneView(ctx, z))
		}
		return out, nil
	})

	huma.Register(api, op("create-zone", http.MethodPost, "/spaces/{space}/zones", "Ask to create a zone. The zone exists after a person allows the card.", "computers"), func(ctx context.Context, in *struct {
		SpacePath
		Body zoneBody
	}) (*struct{ Body CardReply }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		zin, err := in.Body.input()
		if err != nil {
			return nil, err
		}
		if err := a.Zones.Check(ctx, in.Space, zin); err != nil {
			return nil, zoneError(err)
		}
		preview := map[string]string{"name": zin.Name, "network": zin.Network, "size": zin.Size}
		if len(zin.AllowHosts) > 0 {
			preview["allowed sites"] = strings.Join(zin.AllowHosts, ", ")
		}
		msg, err := a.Agent.PostChange(ctx, actorOf(p), in.Space, "zone.create", "Create a zone", "A zone is a separate computer with its own logins.", preview, zin)
		if err != nil {
			return nil, err
		}
		return &struct{ Body CardReply }{Body: CardReply{CardID: msg.Card.ID, ChatID: msg.ChatID.String(), MessageID: msg.ID().String()}}, nil
	})

	huma.Register(api, op("update-zone", http.MethodPatch, "/spaces/{space}/zones/{zone}", "Change a zone. A change of the provider or the network replaces its computer.", "computers"), func(ctx context.Context, in *struct {
		ZonePath
		Body zoneBody
	}) (*struct{ Body ZoneView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		zin, err := in.Body.input()
		if err != nil {
			return nil, err
		}
		old, err := a.Zones.Get(ctx, in.Space, in.Zone)
		if err != nil {
			return nil, zoneError(err)
		}
		z, err := a.Zones.Update(ctx, actorOf(p), in.Space, in.Zone, zin)
		if err != nil {
			return nil, zoneError(err)
		}
		if !sameProvider(old.ProviderID, z.ProviderID) || old.Network != z.Network || !slices.Equal(old.AllowHosts, z.AllowHosts) || old.Size != z.Size {
			// Network rules are set at boot and cannot change inside (SI-05), so the
			// computer is replaced. Its workspaces are checkpointed first.
			if err := a.Computers.Replace(ctx, z.ID()); err != nil {
				return nil, huma.Error500InternalServerError("The zone changed, but its computer could not stop: " + err.Error())
			}
		}
		return &struct{ Body ZoneView }{Body: a.zoneView(ctx, z)}, nil
	})

	huma.Register(api, op("add-zone-grant", http.MethodPost, "/spaces/{space}/zones/{zone}/grants", "Ask to give a zone a vault item or a connector account. The grant exists after a person allows the card.", "computers"), func(ctx context.Context, in *struct {
		ZonePath
		Body struct {
			Kind     string    `json:"kind" enum:"vault_item,connector_account"`
			TargetID uuid.UUID `json:"target_id"`
		}
	}) (*struct{ Body CardReply }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		z, err := a.Zones.Get(ctx, in.Space, in.Zone)
		if err != nil {
			return nil, zoneError(err)
		}
		preview := map[string]string{"zone": z.Name}
		if in.Body.Kind == "vault_item" {
			it, err := a.Vault.Get(ctx, in.Space, in.Body.TargetID)
			if err != nil {
				return nil, apiError(err)
			}
			preview["vault item"] = it.Name
		} else {
			preview["connector account"] = in.Body.TargetID.String()
		}
		msg, err := a.Agent.PostChange(ctx, actorOf(p), in.Space, "grant.add", "Give a zone access", "Bots in this zone can use this access.", preview,
			map[string]any{"zone": z.ID(), "kind": in.Body.Kind, "target": in.Body.TargetID})
		if err != nil {
			return nil, err
		}
		return &struct{ Body CardReply }{Body: CardReply{CardID: msg.Card.ID, ChatID: msg.ChatID.String(), MessageID: msg.ID().String()}}, nil
	})

	type provOut struct {
		Body struct {
			Providers []ComputerProviderView `json:"providers"`
		}
	}
	huma.Register(api, op("list-computer-providers", http.MethodGet, "/spaces/{space}/computer-providers", "List the computer providers of this server", "computers"), func(ctx context.Context, in *SpacePath) (*provOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		rows, err := a.Computers.ProviderRows(ctx)
		if err != nil {
			return nil, err
		}
		out := &provOut{}
		out.Body.Providers = []ComputerProviderView{}
		for _, r := range rows {
			_, avail := a.Computers.Providers[r.Kind]
			out.Body.Providers = append(out.Body.Providers, ComputerProviderView{ID: r.ID().String(), Kind: r.Kind, Name: r.Name, Enabled: r.Enabled, Available: avail, Default: r.Kind == a.Computers.Default, Caps: r.Caps})
		}
		return out, nil
	})

	huma.Register(api, op("enable-host-provider", http.MethodPost, "/spaces/{space}/computer-providers/host/enable", "Ask to turn on the host provider. It is on after a person allows the card.", "computers"), func(ctx context.Context, in *SpacePath) (*struct{ Body CardReply }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		msg, err := a.Agent.PostChange(ctx, actorOf(p), in.Space, "provider.enable_host", "Turn on the host provider",
			"Bots then run commands on this computer itself, with your user rights. Nothing isolates them.", map[string]string{"provider": "host"}, struct{}{})
		if err != nil {
			return nil, err
		}
		return &struct{ Body CardReply }{Body: CardReply{CardID: msg.Card.ID, ChatID: msg.ChatID.String(), MessageID: msg.ID().String()}}, nil
	})
}

func sameProvider(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func zoneError(err error) error {
	switch {
	case errors.Is(err, zones.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, zones.ErrNameTaken):
		return huma.Error409Conflict("A zone with this name exists.")
	case errors.Is(err, zones.ErrNoAllowlist), errors.Is(err, zones.ErrBadZone), errors.Is(err, zones.ErrProviderOff):
		return huma.Error422UnprocessableEntity(zoneMessage(err))
	}
	return err
}

func zoneMessage(err error) string {
	switch {
	case errors.Is(err, zones.ErrNoAllowlist):
		return "This provider cannot limit the network. Choose another provider, or allow all sites."
	case errors.Is(err, zones.ErrProviderOff):
		return "This provider is off. Turn it on first."
	}
	return err.Error()
}
