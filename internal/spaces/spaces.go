//go:build !drelgen

// Package spaces keeps spaces and their members.
package spaces

import (
	"context"
	"errors"
	"strings"

	"github.com/alternayte/drel"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/google/uuid"
)

// Roles in a space.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// ErrNotMember means the user has no role in the space.
var ErrNotMember = errors.New("not a member of this space")

// Service manages spaces.
type Service struct {
	e     *drel.Engine
	audit *audit.Log
}

// New returns the spaces service.
func New(e *drel.Engine, a *audit.Log) *Service { return &Service{e: e, audit: a} }

// Create adds a space with owner as its owner.
func (s *Service) Create(ctx context.Context, actor audit.Actor, name, timeZone string, owner uuid.UUID) (*Space, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("a space needs a name")
	}
	if timeZone == "" {
		timeZone = "UTC"
	}
	sp := &Space{Name: name, TimeZone: timeZone}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.Repo(tx, SpaceMeta).Add(sp)
		drel.Repo(tx, SpaceMemberMeta).Add(&SpaceMember{SpaceID: sp.ID(), UserID: owner, Role: RoleOwner})
		id := sp.ID()
		return s.audit.Record(ctx, actor, &id, "space.create", "space:"+id.String(), map[string]string{"name": name})
	})
	return sp, err
}

// Get returns a space.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Space, error) {
	sp, err := drel.NewRepository(s.e, SpaceMeta).Where(Spaces.ID.Eq(id)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if sp == nil {
		return nil, ErrNotMember
	}
	return sp, nil
}

// Update changes the name and time zone of a space.
func (s *Service) Update(ctx context.Context, actor audit.Actor, id uuid.UUID, name, timeZone string) (*Space, error) {
	var out *Space
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		sp, err := drel.Repo(drel.MustFromContext(ctx), SpaceMeta).Where(Spaces.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if sp == nil {
			return ErrNotMember
		}
		if strings.TrimSpace(name) != "" {
			sp.Name = strings.TrimSpace(name)
		}
		if timeZone != "" {
			sp.TimeZone = timeZone
		}
		out = sp
		return s.audit.Record(ctx, actor, &id, "space.update", "space:"+id.String(), map[string]string{"name": sp.Name, "time_zone": sp.TimeZone})
	})
	return out, err
}

// Membership is a space with the user's role.
type Membership struct {
	Space *Space
	Role  string
}

// ForUser returns the spaces of a user.
func (s *Service) ForUser(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	ms, err := drel.NewRepository(s.e, SpaceMemberMeta).Where(SpaceMembers.UserID.Eq(userID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Membership, 0, len(ms))
	for _, m := range ms {
		sp, err := s.Get(ctx, m.SpaceID)
		if err != nil {
			continue
		}
		out = append(out, Membership{Space: sp, Role: m.Role})
	}
	return out, nil
}

// Role returns the role of a user in a space, or ErrNotMember.
func (s *Service) Role(ctx context.Context, spaceID, userID uuid.UUID) (string, error) {
	m, err := drel.NewRepository(s.e, SpaceMemberMeta).Where(SpaceMembers.SpaceID.Eq(spaceID)).Where(SpaceMembers.UserID.Eq(userID)).FirstOrNil(ctx)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", ErrNotMember
	}
	return m.Role, nil
}
