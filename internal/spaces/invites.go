package spaces

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
)

// Errors of invites and members.
var (
	ErrInviteInvalid = errors.New("this invite link is not valid: it is used, revoked or expired")
	ErrLastOwner     = errors.New("a space keeps at least one owner")
	ErrBadRole       = errors.New("the role is owner or member")
)

// InviteTTL is how long an invite works when the owner names no time.
const InviteTTL = 7 * 24 * time.Hour

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func validRole(r string) bool { return r == RoleOwner || r == RoleMember }

// CreateInvite makes a single-use invite link token for a space. The token is returned
// once; only its hash is kept.
func (s *Service) CreateInvite(ctx context.Context, actor audit.Actor, spaceID, by uuid.UUID, role string, ttl time.Duration) (string, *Invite, error) {
	if !validRole(role) {
		return "", nil, ErrBadRole
	}
	if ttl <= 0 {
		ttl = InviteTTL
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token := "inv_" + base64.RawURLEncoding.EncodeToString(b)
	inv := &Invite{SpaceID: spaceID, TokenHash: hashToken(token), Role: role, CreatedBy: by, ExpiresAt: time.Now().Add(ttl).UTC()}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.Repo(tx, InviteMeta).Add(inv)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "invite.create", "invite:"+inv.ID().String(), map[string]string{"role": role})
	})
	return token, inv, err
}

// Invites lists the invites of a space that still work, newest first.
func (s *Service) Invites(ctx context.Context, spaceID uuid.UUID) ([]*Invite, error) {
	list, err := drel.NewRepository(s.e, InviteMeta).Where(Invites.SpaceID.Eq(spaceID)).OrderBy(Invites.CreatedAt.Desc()).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []*Invite
	now := time.Now()
	for _, inv := range list {
		if inv.AcceptedAt == nil && inv.RevokedAt == nil && inv.ExpiresAt.After(now) {
			out = append(out, inv)
		}
	}
	return out, nil
}

// RevokeInvite stops an invite.
func (s *Service) RevokeInvite(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		inv, err := drel.Repo(tx, InviteMeta).Where(Invites.ID.Eq(id)).Where(Invites.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if inv == nil {
			return ErrInviteInvalid
		}
		now := time.Now().UTC()
		inv.RevokedAt = &now
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "invite.revoke", "invite:"+id.String(), nil)
	})
}

// FindInvite returns the invite of a token that still works, with its space.
func (s *Service) FindInvite(ctx context.Context, token string) (*Invite, *Space, error) {
	inv, err := drel.NewRepository(s.e, InviteMeta).Where(Invites.TokenHash.Eq(hashToken(token))).FirstOrNil(ctx)
	if err != nil {
		return nil, nil, err
	}
	if inv == nil || inv.AcceptedAt != nil || inv.RevokedAt != nil || !inv.ExpiresAt.After(time.Now()) {
		return nil, nil, ErrInviteInvalid
	}
	sp, err := s.Get(ctx, inv.SpaceID)
	if err != nil {
		return nil, nil, err
	}
	return inv, sp, nil
}

// AcceptInvite adds the user to the invite's space with its role and uses the invite up.
// A member who accepts an owner invite becomes an owner; an owner stays an owner. It joins
// the transaction in ctx.
func (s *Service) AcceptInvite(ctx context.Context, token string, userID uuid.UUID) (*Space, string, error) {
	var sp *Space
	var role string
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		inv, err := drel.Repo(tx, InviteMeta).Where(Invites.TokenHash.Eq(hashToken(token))).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if inv == nil || inv.AcceptedAt != nil || inv.RevokedAt != nil || !inv.ExpiresAt.After(time.Now()) {
			return ErrInviteInvalid
		}
		now := time.Now().UTC()
		inv.AcceptedAt, inv.AcceptedBy = &now, &userID
		m, err := drel.Repo(tx, SpaceMemberMeta).Where(SpaceMembers.SpaceID.Eq(inv.SpaceID)).Where(SpaceMembers.UserID.Eq(userID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		role = inv.Role
		switch {
		case m == nil:
			drel.Repo(tx, SpaceMemberMeta).Add(&SpaceMember{SpaceID: inv.SpaceID, UserID: userID, Role: inv.Role})
		case m.Role == RoleOwner:
			role = RoleOwner
		default:
			m.Role = inv.Role
		}
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		sid := inv.SpaceID
		if err := s.audit.Record(ctx, audit.Actor{Kind: "user", ID: userID.String()}, &sid, "invite.accept", "invite:"+inv.ID().String(), map[string]string{"role": role}); err != nil {
			return err
		}
		got, err := drel.Repo(tx, SpaceMeta).Where(Spaces.ID.Eq(sid)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		sp = got
		return nil
	})
	return sp, role, err
}

// Member is one person of a space.
type Member struct {
	UserID uuid.UUID
	Email  string
	Name   string
	Role   string
	Since  time.Time
}

// Members lists the people of a space, owners first.
func (s *Service) Members(ctx context.Context, spaceID uuid.UUID) ([]Member, error) {
	rows, err := s.e.Query(ctx, `SELECT m.user_id, u.email, u.name, m.role, m.created_at FROM space_members m JOIN users u ON u.id = m.user_id
		WHERE m.space_id = $1 ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, u.name, u.email`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.Role, &m.Since); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetRole changes the role of a member. A space keeps at least one owner.
func (s *Service) SetRole(ctx context.Context, actor audit.Actor, spaceID, userID uuid.UUID, role string) error {
	if !validRole(role) {
		return ErrBadRole
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		m, err := drel.Repo(tx, SpaceMemberMeta).Where(SpaceMembers.SpaceID.Eq(spaceID)).Where(SpaceMembers.UserID.Eq(userID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			return ErrNotMember
		}
		if m.Role == RoleOwner && role != RoleOwner {
			if err := s.keepOwner(ctx, tx, spaceID); err != nil {
				return err
			}
		}
		m.Role = role
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "member.role", "user:"+userID.String(), map[string]string{"role": role})
	})
}

// AddMember puts a person in a space with a role; a member keeps their role. A
// group mapping of the Enterprise build uses it.
func (s *Service) AddMember(ctx context.Context, actor audit.Actor, spaceID, userID uuid.UUID, role string) error {
	if !validRole(role) {
		return ErrBadRole
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		m, err := drel.Repo(tx, SpaceMemberMeta).Where(SpaceMembers.SpaceID.Eq(spaceID)).Where(SpaceMembers.UserID.Eq(userID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if m != nil {
			return nil
		}
		drel.Repo(tx, SpaceMemberMeta).Add(&SpaceMember{SpaceID: spaceID, UserID: userID, Role: role})
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "member.add", "user:"+userID.String(), map[string]string{"role": role})
	})
}

// RemoveMember takes a person out of a space. A space keeps at least one owner.
func (s *Service) RemoveMember(ctx context.Context, actor audit.Actor, spaceID, userID uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		m, err := drel.Repo(tx, SpaceMemberMeta).Where(SpaceMembers.SpaceID.Eq(spaceID)).Where(SpaceMembers.UserID.Eq(userID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			return ErrNotMember
		}
		if m.Role == RoleOwner {
			if err := s.keepOwner(ctx, tx, spaceID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM space_members WHERE space_id = $1 AND user_id = $2", spaceID, userID); err != nil {
			return err
		}
		return s.audit.Record(ctx, actor, &spaceID, "member.remove", "user:"+userID.String(), nil)
	})
}

// keepOwner fails when the space has only one owner.
func (s *Service) keepOwner(ctx context.Context, tx *drel.Tx, spaceID uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM space_members WHERE space_id = $1 AND role = 'owner'", spaceID).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastOwner
	}
	return nil
}
