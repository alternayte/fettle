package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/routines"
	"github.com/alternayte/fettle/internal/tools"
)

// registerSelfTools adds attach_file and the admin tools that change a bot's own
// routines and profile (Appendix E). Admin tools go through the approval policy.
func (a *App) registerSelfTools() {
	r := a.Agent.Tools
	r.Add(agent.Tool{
		Name:          "attach_file",
		Description:   "Send a file from your computer to the user's chat, as an attachment of a message.",
		Class:         envelopes.ClassRead,
		NeedsComputer: true,
		NoSubagent:    true,
		Schema:        json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"note":{"type":"string","description":"A short message with the file."}},"required":["path"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in struct{ Path, Note string }
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			if env.ChatID == nil {
				return agent.ToolOutput{}, errors.New("this work has no chat")
			}
			b, err := a.Bots.Get(ctx, env.SpaceID, env.BotID)
			if err != nil || b.ZoneID == nil {
				return agent.ToolOutput{}, tools.ErrNoZone
			}
			home := computers.BotHome(env.BotID)
			full, err := tools.Resolve(home, in.Path)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			l, err := a.Computers.Acquire(ctx, *b.ZoneID)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			data, err := l.Client.ReadFile(ctx, full)
			l.Release()
			if err != nil {
				return agent.ToolOutput{}, err
			}
			limit := int64(a.Settings.Int(ctx, &env.SpaceID, "chat.attachment_max_mb")) << 20
			if int64(len(data)) > limit {
				return agent.ToolOutput{}, fmt.Errorf("the file is larger than %d MB", limit>>20)
			}
			name := path.Base(full)
			id := uuid.Must(uuid.NewV7()).String()
			typ := mime.TypeByExtension(path.Ext(name))
			if typ == "" {
				typ = "application/octet-stream"
			}
			at := chats.Attachment{ID: id, Name: name, Size: int64(len(data)), Type: typ, ObjectKey: attachmentKey(env.SpaceID, *env.ChatID, id)}
			sealed, err := a.Vault.Seal(ctx, env.SpaceID, data, at.ObjectKey)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			if err := a.Objects.Put(ctx, at.ObjectKey, bytes.NewReader(sealed), int64(len(sealed))); err != nil {
				return agent.ToolOutput{}, err
			}
			meta, _ := json.Marshal(at)
			if err := a.Objects.Put(ctx, at.ObjectKey+".json", bytes.NewReader(meta), int64(len(meta))); err != nil {
				return agent.ToolOutput{}, err
			}
			msg := &chats.Message{SpaceID: env.SpaceID, ChatID: *env.ChatID, AuthorKind: "bot", AuthorID: env.BotID.String(), Body: strings.TrimSpace(in.Note), Attachments: []chats.Attachment{at}, EnvelopeID: &env.EnvelopeID}
			botID := env.BotID
			if err := a.Chats.Insert(ctx, msg, &botID); err != nil {
				return agent.ToolOutput{}, err
			}
			a.Chats.Publish(*env.ChatID, "message", a.Agent.MessageView(ctx, msg))
			return agent.ToolOutput{Text: fmt.Sprintf("Sent %s (%d bytes) to the chat.", name, len(data))}, nil
		},
	})
	routineSchema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"cron":{"type":"string","description":"Five fields, for example 0 9 * * 1-5."},"tz":{"type":"string"},"prompt":{"type":"string"},"notify":{"type":"string","enum":["always","on_failure","never"]},"enabled":{"type":"boolean"}},"required":["name"],"additionalProperties":false}`)
	type routineArgs struct {
		Name    string `json:"name"`
		Cron    string `json:"cron"`
		TZ      string `json:"tz"`
		Prompt  string `json:"prompt"`
		Notify  string `json:"notify"`
		Enabled *bool  `json:"enabled"`
	}
	r.Add(agent.Tool{
		Name:        "create_routine",
		Description: "Make one of your own routines: a prompt that runs on a cron schedule in a time zone.",
		Class:       envelopes.ClassAdmin,
		NoSubagent:  true,
		Schema:      routineSchema,
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in routineArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			if in.TZ == "" {
				if sp, err := a.Spaces.Get(ctx, env.SpaceID); err == nil {
					in.TZ = sp.TimeZone
				}
			}
			if in.Notify == "" {
				in.Notify = "on_failure"
			}
			on := in.Enabled == nil || *in.Enabled
			rt, _, err := a.Routines.Create(ctx, env.Actor, env.SpaceID, routines.Input{BotID: env.BotID, Name: in.Name, Prompt: in.Prompt, Trigger: "cron", Cron: in.Cron, TZ: in.TZ, Notify: in.Notify, Enabled: on})
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: fmt.Sprintf("Made the routine %s: %s (%s).", rt.Name, rt.Cron, rt.TZ)}, nil
		},
	})
	r.Add(agent.Tool{
		Name:        "update_routine",
		Description: "Change one of your own routines by its name. Fields you leave out stay as they are.",
		Class:       envelopes.ClassAdmin,
		NoSubagent:  true,
		Schema:      routineSchema,
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in routineArgs
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			list, err := a.Routines.List(ctx, env.SpaceID, &env.BotID)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			var rt *routines.Routine
			var names []string
			for _, x := range list {
				names = append(names, x.Name)
				if strings.EqualFold(x.Name, strings.TrimSpace(in.Name)) {
					rt = x
				}
			}
			if rt == nil {
				return agent.ToolOutput{}, fmt.Errorf("you have no routine %q; yours are: %s", in.Name, strings.Join(names, ", "))
			}
			up := routines.Input{BotID: env.BotID, Name: rt.Name, Prompt: rt.Prompt, Trigger: rt.Trigger, Cron: rt.Cron, TZ: rt.TZ, Notify: rt.Notify, Enabled: rt.Enabled}
			if in.Cron != "" {
				up.Cron = in.Cron
			}
			if in.TZ != "" {
				up.TZ = in.TZ
			}
			if in.Prompt != "" {
				up.Prompt = in.Prompt
			}
			if in.Notify != "" {
				up.Notify = in.Notify
			}
			if in.Enabled != nil {
				up.Enabled = *in.Enabled
			}
			if _, err := a.Routines.Update(ctx, env.Actor, env.SpaceID, rt.ID(), up); err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: "Changed the routine " + rt.Name + "."}, nil
		},
	})
	r.Add(agent.Tool{
		Name:        "update_self",
		Description: "Change your own name or avatar.",
		Class:       envelopes.ClassAdmin,
		NoSubagent:  true,
		Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","maxLength":40},"avatar":{"type":"string","maxLength":200}},"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in struct{ Name, Avatar string }
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			b, err := a.Bots.Get(ctx, env.SpaceID, env.BotID)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			up := bots.Input{Name: b.Name, Job: b.Job, PublicDesc: b.PublicDesc, Instructions: b.Instructions, Avatar: b.Avatar}
			if s := strings.TrimSpace(in.Name); s != "" {
				up.Name = s
			}
			if in.Avatar != "" {
				up.Avatar = in.Avatar
			}
			nb, err := a.Bots.Update(ctx, env.Actor, env.SpaceID, env.BotID, up)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: "Your name is now " + nb.Name + "."}, nil
		},
	})
}
