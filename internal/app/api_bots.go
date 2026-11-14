package app

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/zones"
)

// BotView is a bot.
type BotView struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Job             string  `json:"job"`
	PublicDesc      string  `json:"public_desc"`
	Instructions    string  `json:"instructions"`
	InstructionsRev int     `json:"instructions_rev"`
	Avatar          string  `json:"avatar"`
	IsPrimary       bool    `json:"is_primary"`
	Status          string  `json:"status"`
	ZoneID          *string `json:"zone_id"`
	ZoneName        string  `json:"zone_name"`
	ChatID          string  `json:"chat_id"`
	Activity        string  `json:"activity" enum:"idle,working,waiting" doc:"What the bot does now."`
}

func (a *App) botView(ctx context.Context, b *bots.Bot) BotView {
	v := BotView{ID: b.ID().String(), Name: b.Name, Job: b.Job, PublicDesc: b.PublicDesc, Instructions: b.Instructions, InstructionsRev: b.InstructionsRev, Avatar: b.Avatar, IsPrimary: b.IsPrimary, Status: b.Status, Activity: "idle"}
	if b.ZoneID != nil {
		s := b.ZoneID.String()
		v.ZoneID = &s
		if z, err := a.Zones.Get(ctx, b.SpaceID, *b.ZoneID); err == nil {
			v.ZoneName = z.Name
		}
	}
	if c, err := a.Chats.DirectFor(ctx, b.SpaceID, b.ID()); err == nil {
		v.ChatID = c.ID().String()
	}
	var st string
	if err := a.DB.Engine.QueryRow(ctx, "SELECT status FROM runs WHERE bot_id = $1 AND kind = 'owner' AND status IN ('queued','running','verifying','waiting','escalated') ORDER BY created_at DESC LIMIT 1", b.ID()).Scan(&st); err == nil {
		if st == "waiting" || st == "escalated" {
			v.Activity = "waiting"
		} else {
			v.Activity = "working"
		}
	}
	return v
}

type BotPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Bot   uuid.UUID `path:"bot"`
}

type botBody struct {
	Name         string `json:"name" minLength:"1" maxLength:"40"`
	Job          string `json:"job,omitempty" maxLength:"200"`
	PublicDesc   string `json:"public_desc,omitempty" maxLength:"200"`
	Instructions string `json:"instructions,omitempty" maxLength:"20000"`
	Avatar       string `json:"avatar,omitempty" maxLength:"16"`
	// ZoneID places a new bot in a zone (default: personal). Moving a bot later needs a card.
	ZoneID string `json:"zone_id,omitempty" doc:"Only on create."`
}

type botsOut struct {
	Body struct {
		Bots []BotView `json:"bots"`
	}
}

type botOut struct{ Body BotView }

// VersionView is one instructions version.
type VersionView struct {
	Rev          int       `json:"rev"`
	Instructions string    `json:"instructions"`
	AuthorKind   string    `json:"author_kind"`
	AuthorID     string    `json:"author_id"`
	At           time.Time `json:"at"`
	Current      bool      `json:"current"`
}

// MemoryView is MEMORY.md or the shared memory.
type MemoryView struct {
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
	Limit   int    `json:"limit"`
	Rev     int    `json:"rev"`
}

type memoryOut struct{ Body MemoryView }

type MemoryIn struct {
	Body struct {
		Content string `json:"content" maxLength:"8192"`
	}
}

func memView(m *bots.Memory) MemoryView {
	return MemoryView{Content: m.Content, Bytes: len(m.Content), Limit: bots.MemoryLimit, Rev: m.Rev}
}

func (a *App) registerBots(api huma.API) {
	huma.Register(api, op("list-bots", http.MethodGet, "/spaces/{space}/bots", "List the bots of a space", "bots"), func(ctx context.Context, in *struct {
		SpacePath
		Archived bool `query:"archived"`
	}) (*botsOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		list, err := a.Bots.List(ctx, in.Space, in.Archived)
		if err != nil {
			return nil, err
		}
		out := &botsOut{}
		out.Body.Bots = []BotView{}
		for _, b := range list {
			out.Body.Bots = append(out.Body.Bots, a.botView(ctx, b))
		}
		return out, nil
	})

	huma.Register(api, op("create-bot", http.MethodPost, "/spaces/{space}/bots", "Create a bot with its direct chat", "bots"), func(ctx context.Context, in *struct {
		SpacePath
		Body botBody
	}) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		var b *bots.Bot
		err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			var zoneID *uuid.UUID
			if in.Body.ZoneID != "" {
				id, err := uuid.Parse(in.Body.ZoneID)
				if err != nil {
					return huma.Error422UnprocessableEntity("zone_id is not valid")
				}
				if _, err := a.Zones.Get(ctx, in.Space, id); err != nil {
					return huma.Error422UnprocessableEntity("The zone is not in this space.")
				}
				zoneID = &id
			} else if z, err := a.Zones.ByName(ctx, in.Space, zones.Personal); err == nil {
				id := z.ID()
				zoneID = &id
			}
			var err error
			b, err = a.Bots.Create(ctx, actorOf(p), in.Space, bots.Input{Name: in.Body.Name, Job: in.Body.Job, PublicDesc: in.Body.PublicDesc, Instructions: in.Body.Instructions, Avatar: in.Body.Avatar, ZoneID: zoneID})
			if err != nil {
				return err
			}
			_, err = a.Chats.CreateDirect(ctx, in.Space, b.ID(), b.Name)
			return err
		})
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("get-bot", http.MethodGet, "/spaces/{space}/bots/{bot}", "A bot", "bots"), func(ctx context.Context, in *BotPath) (*botOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		b, err := a.Bots.Get(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("update-bot", http.MethodPatch, "/spaces/{space}/bots/{bot}", "Change a bot. Changed instructions become a new version.", "bots"), func(ctx context.Context, in *struct {
		BotPath
		Body botBody
	}) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		b, err := a.Bots.Update(ctx, actorOf(p), in.Space, in.Bot, bots.Input{Name: in.Body.Name, Job: in.Body.Job, PublicDesc: in.Body.PublicDesc, Instructions: in.Body.Instructions, Avatar: in.Body.Avatar})
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("list-instruction-versions", http.MethodGet, "/spaces/{space}/bots/{bot}/instructions", "The instructions history of a bot", "bots"), func(ctx context.Context, in *BotPath) (*struct {
		Body struct {
			Versions []VersionView `json:"versions"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		b, err := a.Bots.Get(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, apiError(err)
		}
		vs, err := a.Bots.Versions(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, apiError(err)
		}
		out := &struct {
			Body struct {
				Versions []VersionView `json:"versions"`
			}
		}{}
		out.Body.Versions = []VersionView{}
		for _, v := range vs {
			out.Body.Versions = append(out.Body.Versions, VersionView{Rev: v.Rev, Instructions: v.Instructions, AuthorKind: v.AuthorKind, AuthorID: v.AuthorID, At: v.CreatedAt(), Current: v.Rev == b.InstructionsRev})
		}
		return out, nil
	})

	huma.Register(api, op("restore-instructions", http.MethodPost, "/spaces/{space}/bots/{bot}/instructions/{rev}/restore", "Make an old instructions version current", "bots"), func(ctx context.Context, in *struct {
		BotPath
		Rev int `path:"rev" minimum:"1"`
	}) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		b, err := a.Bots.Restore(ctx, actorOf(p), in.Space, in.Bot, in.Rev)
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("get-bot-memory", http.MethodGet, "/spaces/{space}/bots/{bot}/memory", "The MEMORY.md of a bot", "memory"), func(ctx context.Context, in *BotPath) (*memoryOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		if _, err := a.Bots.Get(ctx, in.Space, in.Bot); err != nil {
			return nil, apiError(err)
		}
		m, err := a.Bots.GetMemory(ctx, in.Space, &in.Bot)
		if err != nil {
			return nil, err
		}
		return &memoryOut{Body: memView(m)}, nil
	})

	huma.Register(api, op("put-bot-memory", http.MethodPut, "/spaces/{space}/bots/{bot}/memory", "Replace the MEMORY.md of a bot (at most 4 KB)", "memory"), func(ctx context.Context, in *struct {
		BotPath
		MemoryIn
	}) (*memoryOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if _, err := a.Bots.Get(ctx, in.Space, in.Bot); err != nil {
			return nil, apiError(err)
		}
		m, err := a.Bots.SetMemory(ctx, actorOf(p), in.Space, &in.Bot, in.Body.Content, "user")
		if err != nil {
			return nil, apiError(err)
		}
		return &memoryOut{Body: memView(m)}, nil
	})

	huma.Register(api, op("get-shared-memory", http.MethodGet, "/spaces/{space}/memory", "The shared memory of a space", "memory"), func(ctx context.Context, in *SpacePath) (*memoryOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		m, err := a.Bots.GetMemory(ctx, in.Space, nil)
		if err != nil {
			return nil, err
		}
		return &memoryOut{Body: memView(m)}, nil
	})

	huma.Register(api, op("put-shared-memory", http.MethodPut, "/spaces/{space}/memory", "Replace the shared memory (at most 4 KB)", "memory"), func(ctx context.Context, in *struct {
		SpacePath
		MemoryIn
	}) (*memoryOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		m, err := a.Bots.SetMemory(ctx, actorOf(p), in.Space, nil, in.Body.Content, "user")
		if err != nil {
			return nil, apiError(err)
		}
		return &memoryOut{Body: memView(m)}, nil
	})
}
