// Package zones keeps trust zones and their grants (§10.1).
package zones

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
)

// Default zone names.
const (
	Personal = "personal"
	Browsing = "browsing"
)

// Errors of the zones service.
var (
	ErrNotFound     = errors.New("zone not found")
	ErrNoAllowlist  = errors.New("this provider cannot enforce a network allowlist")
	ErrBadZone      = errors.New("a zone needs a name, a size and a network policy")
	ErrGrantMissing = errors.New("this zone has no grant for that item")
	ErrProviderOff  = errors.New("this provider is off")
	ErrNameTaken    = errors.New("a zone with this name exists")
)

// Service manages zones.
type Service struct {
	e     *drel.Engine
	audit *audit.Log
	// CheckProvider refuses a provider that is off, or that cannot enforce the zone's
	// allowlist (REQ-CMP-012). A nil provider ID means the default provider.
	CheckProvider func(ctx context.Context, providerID *uuid.UUID, allowlist bool) error
}

// New returns the zones service.
func New(e *drel.Engine, a *audit.Log) *Service { return &Service{e: e, audit: a} }

// CreateDefaults adds the personal and browsing zones of a new space in the transaction in
// ctx and returns the personal zone.
func (s *Service) CreateDefaults(ctx context.Context, spaceID uuid.UUID) (*Zone, error) {
	personal := &Zone{SpaceID: spaceID, Name: Personal, Network: "open", AllowHosts: []string{}, Size: "browser", IdleMinutes: 10}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		repo := drel.Repo(tx, ZoneMeta)
		repo.Add(personal)
		repo.Add(&Zone{SpaceID: spaceID, Name: Browsing, Network: "open", AllowHosts: []string{}, Size: "browser", IdleMinutes: 10})
		return tx.SaveChanges(ctx)
	})
	return personal, err
}

// List returns the zones of a space.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID) ([]*Zone, error) {
	return drel.NewRepository(s.e, ZoneMeta).Where(Zones.SpaceID.Eq(spaceID)).OrderBy(Zones.Name.Asc()).All(ctx)
}

// Get returns a zone of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Zone, error) {
	z, err := drel.NewRepository(s.e, ZoneMeta).Where(Zones.ID.Eq(id)).Where(Zones.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, ErrNotFound
	}
	return z, nil
}

// Find returns a zone by ID in any space (computer manager only).
func (s *Service) Find(ctx context.Context, id uuid.UUID) (*Zone, error) {
	z, err := drel.NewRepository(s.e, ZoneMeta).Where(Zones.ID.Eq(id)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, ErrNotFound
	}
	return z, nil
}

// ByName returns a zone of a space by name.
func (s *Service) ByName(ctx context.Context, spaceID uuid.UUID, name string) (*Zone, error) {
	var z *Zone
	var err error
	if tx, ok := drel.FromContext(ctx); ok {
		z, err = drel.NewTxRepository(tx, ZoneMeta).Where(Zones.SpaceID.Eq(spaceID)).Where(Zones.Name.Eq(name)).FirstOrNil(ctx)
	} else {
		z, err = drel.NewRepository(s.e, ZoneMeta).Where(Zones.SpaceID.Eq(spaceID)).Where(Zones.Name.Eq(name)).FirstOrNil(ctx)
	}
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, ErrNotFound
	}
	return z, nil
}

// Input changes a zone.
type Input struct {
	Name        string
	ProviderID  *uuid.UUID
	Network     string
	AllowHosts  []string
	Size        string
	IdleMinutes int
	// ShareBlocked stops handoffs from copying this zone's files out.
	ShareBlocked bool
}

func (s *Service) validate(ctx context.Context, in Input) error {
	if strings.TrimSpace(in.Name) == "" || !slices.Contains([]string{"small", "browser", "desktop"}, in.Size) || !slices.Contains([]string{"open", "allowlist"}, in.Network) {
		return ErrBadZone
	}
	if s.CheckProvider != nil {
		if err := s.CheckProvider(ctx, in.ProviderID, in.Network == "allowlist"); err != nil {
			return err
		}
	}
	return nil
}

// Check validates a new zone without storing it.
func (s *Service) Check(ctx context.Context, spaceID uuid.UUID, in Input) error {
	if err := s.validate(ctx, in); err != nil {
		return err
	}
	if _, err := s.ByName(ctx, spaceID, strings.TrimSpace(in.Name)); err == nil {
		return ErrNameTaken
	}
	return nil
}

// Create adds a zone. Bots ask for it through a hard-ask card; the API caller is an owner.
func (s *Service) Create(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in Input) (*Zone, error) {
	if err := s.Check(ctx, spaceID, in); err != nil {
		return nil, err
	}
	if in.IdleMinutes <= 0 {
		in.IdleMinutes = 10
	}
	if in.AllowHosts == nil {
		in.AllowHosts = []string{}
	}
	z := &Zone{SpaceID: spaceID, Name: strings.TrimSpace(in.Name), ProviderID: in.ProviderID, Network: in.Network, AllowHosts: in.AllowHosts, Size: in.Size, IdleMinutes: in.IdleMinutes, ShareBlocked: in.ShareBlocked}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ZoneMeta).Add(z)
		return s.audit.Record(ctx, actor, &spaceID, "zone.create", "zone:"+z.ID().String(), map[string]string{"name": z.Name, "network": z.Network})
	})
	return z, err
}

// Update changes a zone.
func (s *Service) Update(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, in Input) (*Zone, error) {
	if err := s.validate(ctx, in); err != nil {
		return nil, err
	}
	var out *Zone
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		z, err := drel.Repo(drel.MustFromContext(ctx), ZoneMeta).Where(Zones.ID.Eq(id)).Where(Zones.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if z == nil {
			return ErrNotFound
		}
		if in.AllowHosts == nil {
			in.AllowHosts = []string{}
		}
		z.Name, z.ProviderID, z.Network, z.AllowHosts, z.Size = strings.TrimSpace(in.Name), in.ProviderID, in.Network, in.AllowHosts, in.Size
		z.ShareBlocked = in.ShareBlocked
		if in.IdleMinutes > 0 {
			z.IdleMinutes = in.IdleMinutes
		}
		out = z
		return s.audit.Record(ctx, actor, &spaceID, "zone.update", "zone:"+id.String(), map[string]string{"name": z.Name, "network": z.Network, "size": z.Size})
	})
	return out, err
}

// Grants returns the grants of a zone.
func (s *Service) Grants(ctx context.Context, zoneID uuid.UUID) ([]*ZoneGrant, error) {
	return drel.NewRepository(s.e, ZoneGrantMeta).Where(ZoneGrants.ZoneID.Eq(zoneID)).All(ctx)
}

// HasGrant reports whether a zone holds a grant.
func (s *Service) HasGrant(ctx context.Context, zoneID uuid.UUID, kind string, target uuid.UUID) (bool, error) {
	return drel.NewRepository(s.e, ZoneGrantMeta).Where(ZoneGrants.ZoneID.Eq(zoneID)).Where(ZoneGrants.Kind.Eq(kind)).Where(ZoneGrants.TargetID.Eq(target)).Exists(ctx)
}

// AddGrant gives a zone a vault item or a connector account.
func (s *Service) AddGrant(ctx context.Context, actor audit.Actor, spaceID, zoneID uuid.UUID, kind string, target uuid.UUID) error {
	if _, err := s.Get(ctx, spaceID, zoneID); err != nil {
		return err
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ZoneGrantMeta).Add(&ZoneGrant{ZoneID: zoneID, Kind: kind, TargetID: target})
		return s.audit.Record(ctx, actor, &spaceID, "grant.add", "zone:"+zoneID.String(), map[string]string{"kind": kind, "target": target.String()})
	})
}

// RemoveGrant takes a grant away.
func (s *Service) RemoveGrant(ctx context.Context, actor audit.Actor, spaceID, zoneID uuid.UUID, kind string, target uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM zone_grants WHERE zone_id = $1 AND kind = $2 AND target_id = $3", zoneID, kind, target); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "grant.remove", "zone:"+zoneID.String(), map[string]string{"kind": kind, "target": target.String()})
	})
}
