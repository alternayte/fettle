package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alternayte/drel"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/routines"
)

// registerBotLife adds duplicate, archive, restore, delete and the primary change (§6.1, §9.8).
func (a *App) registerBotLife(api huma.API) {
	huma.Register(api, op("duplicate-bot", http.MethodPost, "/spaces/{space}/bots/{bot}/duplicate", "Copy a bot: profile, instructions, zone, model roles, skills and paused routines. Memory, chats and workspace stay with the original.", "bots"), func(ctx context.Context, in *BotPath) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		b, err := a.duplicateBot(ctx, actorOf(p), in.Space, in.Bot)
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("archive-bot", http.MethodPost, "/spaces/{space}/bots/{bot}/archive", "Archive a bot. It keeps everything; its routines stop.", "bots"), func(ctx context.Context, in *BotPath) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		var b *bots.Bot
		err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			var err error
			if b, err = a.Bots.SetArchived(ctx, actorOf(p), in.Space, in.Bot, true); err != nil {
				return err
			}
			list, err := a.Routines.List(ctx, in.Space, &in.Bot)
			if err != nil {
				return err
			}
			for _, r := range list {
				if r.Enabled {
					if err := a.Routines.SetEnabled(ctx, actorOf(p), in.Space, r.ID(), false); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("restore-bot", http.MethodPost, "/spaces/{space}/bots/{bot}/restore", "Bring an archived bot back. Its routines stay paused.", "bots"), func(ctx context.Context, in *BotPath) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		b, err := a.Bots.SetArchived(ctx, actorOf(p), in.Space, in.Bot, false)
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})

	huma.Register(api, op("delete-bot", http.MethodDelete, "/spaces/{space}/bots/{bot}", "Delete a bot with its chats, memory, routines and home workspace. Type the bot's name to confirm.", "bots"), func(ctx context.Context, in *struct {
		BotPath
		Confirm string `query:"confirm" doc:"The bot's name, typed by the user."`
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		b, err := a.Bots.Get(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, apiError(err)
		}
		if strings.TrimSpace(in.Confirm) != b.Name {
			return nil, huma.Error422UnprocessableEntity("Type the bot's name to delete it.")
		}
		if b.IsPrimary {
			return nil, apiError(bots.ErrPrimaryLast)
		}
		err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			for _, q := range []string{
				// Its direct chat goes with its messages; group chats lose the member.
				"DELETE FROM chats WHERE kind = 'direct' AND id IN (SELECT chat_id FROM chat_members WHERE bot_id = $1)",
				"DELETE FROM chat_members WHERE bot_id = $1",
				"DELETE FROM routines WHERE bot_id = $1",
				"DELETE FROM bot_skills WHERE bot_id = $1",
				"DELETE FROM model_roles WHERE bot_id = $1",
				"DELETE FROM screen_leases WHERE bot_id = $1",
			} {
				if _, err := tx.Exec(ctx, q, in.Bot); err != nil {
					return err
				}
			}
			return a.Bots.Delete(ctx, actorOf(p), in.Space, in.Bot)
		})
		if err != nil {
			return nil, apiError(err)
		}
		if err := a.Computers.DeleteBotHome(ctx, in.Bot, b.ZoneID); err != nil {
			a.Log.Warn("delete bot home", "bot", in.Bot, "error", err)
		}
		return nil, nil
	})

	huma.Register(api, op("make-primary", http.MethodPost, "/spaces/{space}/bots/{bot}/primary", "Ask to make a bot the primary bot. The change needs a card in the primary bot's chat.", "bots"), func(ctx context.Context, in *BotPath) (*struct{ Body CardReply }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		b, err := a.Bots.Get(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, apiError(err)
		}
		if b.IsPrimary {
			return nil, huma.Error409Conflict(b.Name + " is the primary bot already.")
		}
		msg, err := a.Agent.PostChange(ctx, actorOf(p), in.Space, "bot.primary", "Make "+b.Name+" the primary bot",
			"The primary bot answers messages that name no bot, proposes bots and sends the daily digest. Only one bot has the role.",
			map[string]string{"bot": b.Name}, map[string]any{"bot": b.ID()})
		if err != nil {
			return nil, err
		}
		return &struct{ Body CardReply }{Body: CardReply{CardID: msg.Card.ID, ChatID: msg.ChatID.String(), MessageID: msg.ID().String()}}, nil
	})
}

func (a *App) registerPrimaryChange() {
	a.Agent.RegisterChange("bot.primary", func(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, raw json.RawMessage) error {
		var c struct {
			Bot uuid.UUID `json:"bot"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		return a.Bots.SetPrimary(ctx, actor, spaceID, c.Bot)
	})
}

// duplicateBot copies a bot as §6.1 says. The copy gets a free name: "Name 2", "Name 3".
func (a *App) duplicateBot(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) (*bots.Bot, error) {
	src, err := a.Bots.Get(ctx, spaceID, id)
	if err != nil {
		return nil, err
	}
	var out *bots.Bot
	err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
		var b *bots.Bot
		for n := 2; n < 100; n++ {
			name := fmt.Sprintf("%s %d", src.Name, n)
			if len([]rune(name)) > 40 {
				name = fmt.Sprintf("%s %d", string([]rune(src.Name)[:36]), n)
			}
			b, err = a.Bots.Create(ctx, actor, spaceID, bots.Input{Name: name, Job: src.Job, PublicDesc: src.PublicDesc, Instructions: src.Instructions, Avatar: src.Avatar, ZoneID: src.ZoneID})
			if err == nil || !errors.Is(err, bots.ErrNameTaken) {
				break
			}
		}
		if err != nil {
			return err
		}
		if _, err := a.Chats.CreateDirect(ctx, spaceID, b.ID(), b.Name); err != nil {
			return err
		}
		roles, err := a.Models.Roles(ctx, spaceID, &id)
		if err != nil {
			return err
		}
		for _, r := range roles {
			if r.BotID != nil && *r.BotID == id {
				if err := a.Models.SetRole(ctx, actor, spaceID, ptrTo(b.ID()), r.Role, r.Targets); err != nil {
					return err
				}
			}
		}
		skillList, err := a.Skills.ForBot(ctx, spaceID, id)
		if err != nil {
			return err
		}
		for _, sk := range skillList {
			if err := a.Skills.Assign(ctx, actor, spaceID, b.ID(), sk.ID(), true); err != nil {
				return err
			}
		}
		list, err := a.Routines.List(ctx, spaceID, &id)
		if err != nil {
			return err
		}
		for _, r := range list {
			if r.Trigger != "cron" {
				continue // a webhook copy would need its own secret; the user adds it
			}
			if _, _, err := a.Routines.Create(ctx, actor, spaceID, routines.Input{BotID: b.ID(), Name: r.Name, Prompt: r.Prompt, Trigger: r.Trigger, Cron: r.Cron, TZ: r.TZ, Notify: r.Notify, Enabled: false}); err != nil {
				return err
			}
		}
		out = b
		return nil
	})
	return out, err
}

func ptrTo[T any](v T) *T { return &v }
