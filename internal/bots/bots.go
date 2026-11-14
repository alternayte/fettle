//go:build !drelgen

// Package bots keeps bots, their instruction history, memory and open work.
package bots

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alternayte/drel"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/google/uuid"
)

// Errors of the bots service.
var (
	ErrNotFound    = errors.New("bot not found")
	ErrLimit       = errors.New("this space has the maximum number of bots")
	ErrNameTaken   = errors.New("another bot in this space has this name")
	ErrBadName     = errors.New("a bot name has 1 to 40 characters")
	ErrNoVersion   = errors.New("this instructions version does not exist")
	ErrPrimaryLast = errors.New("a space needs one primary bot")
)

// Service manages bots.
type Service struct {
	e     *drel.Engine
	audit *audit.Log
	// MaxPerSpace returns the bot limit of a space (setting bots.max_per_space).
	MaxPerSpace func(ctx context.Context, spaceID uuid.UUID) int
}

// New returns the bots service.
func New(e *drel.Engine, a *audit.Log) *Service {
	return &Service{e: e, audit: a, MaxPerSpace: func(context.Context, uuid.UUID) int { return 50 }}
}

// Input creates or changes a bot.
type Input struct {
	Name         string
	Job          string
	Instructions string
	PublicDesc   string
	Avatar       string
	ZoneID       *uuid.UUID
	IsPrimary    bool
}

func cleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > 40 || strings.ContainsAny(s, "@\n\t") {
		return "", ErrBadName
	}
	return s, nil
}

// Create adds a bot with instructions version 1.
func (s *Service) Create(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in Input) (*Bot, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return nil, err
	}
	b := &Bot{SpaceID: spaceID, Name: name, Job: strings.TrimSpace(in.Job), Instructions: in.Instructions, InstructionsRev: 1, ZoneID: in.ZoneID, Avatar: in.Avatar, IsPrimary: in.IsPrimary, PublicDesc: strings.TrimSpace(in.PublicDesc), Status: "active", CreatedByKind: actor.Kind, CreatedBy: actor.ID}
	if b.PublicDesc == "" {
		b.PublicDesc = b.Job
	}
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		repo := drel.Repo(tx, BotMeta)
		n, err := repo.Where(Bots.SpaceID.Eq(spaceID)).Where(Bots.Status.Eq("active")).Count(ctx)
		if err != nil {
			return err
		}
		if n >= s.MaxPerSpace(ctx, spaceID) {
			return ErrLimit
		}
		taken, err := repo.Where(Bots.SpaceID.Eq(spaceID)).Where(Bots.Name.Eq(name)).Where(Bots.Status.Eq("active")).Exists(ctx)
		if err != nil {
			return err
		}
		if taken {
			return ErrNameTaken
		}
		if b.IsPrimary {
			if _, err := tx.Exec(ctx, "UPDATE bots SET is_primary = $1 WHERE space_id = $2", false, spaceID); err != nil {
				return err
			}
		}
		repo.Add(b)
		drel.Repo(tx, InstructionVersionMeta).Add(&InstructionVersion{BotID: b.ID(), Rev: 1, Instructions: b.Instructions, AuthorKind: actor.Kind, AuthorID: actor.ID})
		return s.audit.Record(ctx, actor, &spaceID, "bot.create", "bot:"+b.ID().String(), map[string]string{"name": b.Name})
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Get returns a bot of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Bot, error) {
	b, err := drel.NewRepository(s.e, BotMeta).Where(Bots.ID.Eq(id)).Where(Bots.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrNotFound
	}
	return b, nil
}

// ByName finds an active bot by name, ignoring case.
func (s *Service) ByName(ctx context.Context, spaceID uuid.UUID, name string) (*Bot, error) {
	list, err := s.List(ctx, spaceID, false)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		if strings.EqualFold(b.Name, name) {
			return b, nil
		}
	}
	return nil, ErrNotFound
}

// List returns the bots of a space, primary first.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID, archived bool) ([]*Bot, error) {
	q := drel.NewRepository(s.e, BotMeta).Where(Bots.SpaceID.Eq(spaceID))
	if !archived {
		q = q.Where(Bots.Status.Eq("active"))
	}
	return q.OrderBy(Bots.IsPrimary.Desc(), Bots.Name.Asc()).All(ctx)
}

// Primary returns the primary bot of a space.
func (s *Service) Primary(ctx context.Context, spaceID uuid.UUID) (*Bot, error) {
	b, err := drel.NewRepository(s.e, BotMeta).Where(Bots.SpaceID.Eq(spaceID)).Where(Bots.IsPrimary.Eq(true)).Where(Bots.Status.Eq("active")).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrNotFound
	}
	return b, nil
}

// Update changes profile fields and, when they differ, the instructions as a new version.
func (s *Service) Update(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, in Input) (*Bot, error) {
	var out *Bot
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		repo := drel.Repo(tx, BotMeta)
		b, err := repo.Where(Bots.ID.Eq(id)).Where(Bots.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if b == nil {
			return ErrNotFound
		}
		if in.Name != "" && in.Name != b.Name {
			name, err := cleanName(in.Name)
			if err != nil {
				return err
			}
			taken, err := repo.Where(Bots.SpaceID.Eq(spaceID)).Where(Bots.Name.Eq(name)).Where(Bots.Status.Eq("active")).Where(Bots.ID.NEQ(id)).Exists(ctx)
			if err != nil {
				return err
			}
			if taken {
				return ErrNameTaken
			}
			b.Name = name
		}
		b.Job, b.PublicDesc, b.Avatar = strings.TrimSpace(in.Job), strings.TrimSpace(in.PublicDesc), in.Avatar
		if b.PublicDesc == "" {
			b.PublicDesc = b.Job
		}
		details := map[string]string{"name": b.Name}
		if in.Instructions != b.Instructions {
			var maxRev int
			if err := tx.QueryRow(ctx, "SELECT COALESCE(MAX(rev), 0) FROM bot_instructions WHERE bot_id = $1", id).Scan(&maxRev); err != nil {
				return err
			}
			b.InstructionsRev = maxRev + 1
			b.Instructions = in.Instructions
			drel.Repo(tx, InstructionVersionMeta).Add(&InstructionVersion{BotID: id, Rev: b.InstructionsRev, Instructions: in.Instructions, AuthorKind: actor.Kind, AuthorID: actor.ID})
			details["instructions_rev"] = fmt.Sprint(b.InstructionsRev)
		}
		out = b
		return s.audit.Record(ctx, actor, &spaceID, "bot.update", "bot:"+id.String(), details)
	})
	return out, err
}

// Versions returns the instruction history, newest first.
func (s *Service) Versions(ctx context.Context, spaceID, id uuid.UUID) ([]*InstructionVersion, error) {
	if _, err := s.Get(ctx, spaceID, id); err != nil {
		return nil, err
	}
	return drel.NewRepository(s.e, InstructionVersionMeta).Where(InstructionVersions.BotID.Eq(id)).OrderBy(InstructionVersions.Rev.Desc()).All(ctx)
}

// Restore makes an old version current. The history keeps every version; the bot
// points at the restored one.
func (s *Service) Restore(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, rev int) (*Bot, error) {
	var out *Bot
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		v, err := drel.Repo(tx, InstructionVersionMeta).Where(InstructionVersions.BotID.Eq(id)).Where(InstructionVersions.Rev.Eq(rev)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if v == nil {
			return ErrNoVersion
		}
		b, err := drel.Repo(tx, BotMeta).Where(Bots.ID.Eq(id)).Where(Bots.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if b == nil {
			return ErrNotFound
		}
		b.Instructions, b.InstructionsRev = v.Instructions, v.Rev
		out = b
		return s.audit.Record(ctx, actor, &spaceID, "bot.restore_instructions", "bot:"+id.String(), map[string]string{"rev": fmt.Sprint(rev)})
	})
	return out, err
}
