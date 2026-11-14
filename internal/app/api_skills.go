package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/skills"
)

// SkillView is a skill of the library.
type SkillView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Source         string   `json:"source" enum:"local,skael"`
	CurrentVersion int      `json:"current_version"`
	Bots           []string `json:"bots" doc:"IDs of the bots that have this skill."`
}

// SkillVersionView is one version of a skill.
type SkillVersionView struct {
	Version   int       `json:"version"`
	SHA256    string    `json:"sha256"`
	Files     []string  `json:"files"`
	HasEvals  bool      `json:"has_evals"`
	Upstream  int       `json:"upstream,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SkillMD   string    `json:"skill_md"`
}

type SkillPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Skill uuid.UUID `path:"skill"`
}

func skillError(err error) error {
	switch {
	case errors.Is(err, skills.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, skills.ErrNoMD), errors.Is(err, skills.ErrBadMD), errors.Is(err, skills.ErrTooLarge), errors.Is(err, skills.ErrNoSkael):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return err
}

func (a *App) skillView(ctx context.Context, sk *skills.Skill) SkillView {
	v := SkillView{ID: sk.ID().String(), Name: sk.Name, Description: sk.Description, Source: sk.Source, CurrentVersion: sk.CurrentVersion, Bots: []string{}}
	rows, err := a.DB.Engine.Query(ctx, "SELECT bot_id FROM bot_skills WHERE skill_id = $1", sk.ID())
	if err == nil {
		for rows.Next() {
			var id uuid.UUID
			if rows.Scan(&id) == nil {
				v.Bots = append(v.Bots, id.String())
			}
		}
		rows.Close()
	}
	return v
}

func (a *App) registerSkills(api huma.API) {
	type listOut struct {
		Body struct {
			Skills []SkillView `json:"skills"`
		}
	}
	huma.Register(api, op("list-skills", http.MethodGet, "/spaces/{space}/skills", "List the skill library of a space", "skills"), func(ctx context.Context, in *SpacePath) (*listOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		list, err := a.Skills.List(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Skills = []SkillView{}
		for _, sk := range list {
			out.Body.Skills = append(out.Body.Skills, a.skillView(ctx, sk))
		}
		return out, nil
	})

	huma.Register(api, op("get-skill", http.MethodGet, "/spaces/{space}/skills/{skill}", "A skill with its version history", "skills"), func(ctx context.Context, in *SkillPath) (*struct {
		Body struct {
			Skill    SkillView          `json:"skill"`
			Versions []SkillVersionView `json:"versions"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		sk, err := a.Skills.Get(ctx, in.Space, in.Skill)
		if err != nil {
			return nil, skillError(err)
		}
		vs, err := a.Skills.Versions(ctx, sk.ID())
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Skill    SkillView          `json:"skill"`
				Versions []SkillVersionView `json:"versions"`
			}
		}{}
		out.Body.Skill = a.skillView(ctx, sk)
		out.Body.Versions = []SkillVersionView{}
		for _, v := range vs {
			out.Body.Versions = append(out.Body.Versions, SkillVersionView{Version: v.Version, SHA256: v.SHA256, Files: v.Files, HasEvals: v.HasEvals, Upstream: v.Upstream, CreatedAt: v.CreatedAt(), SkillMD: v.SkillMD})
		}
		return out, nil
	})

	huma.Register(api, op("import-skill", http.MethodPost, "/spaces/{space}/skills/import", "Import a skill from a zip of its folder. The same name makes a new version.", "skills"), func(ctx context.Context, in *struct {
		SpacePath
		RawBody []byte `contentType:"application/zip"`
	}) (*struct{ Body SkillView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		files, err := skills.FromZip(in.RawBody)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		sk, _, err := a.Skills.Store(ctx, actorOf(p), in.Space, files, "local", 0)
		if err != nil {
			return nil, skillError(err)
		}
		return &struct{ Body SkillView }{Body: a.skillView(ctx, sk)}, nil
	})

	huma.Register(api, op("save-skill-md", http.MethodPost, "/spaces/{space}/skills", "Save a skill from its SKILL.md text (Save as skill). The same name makes a new version.", "skills"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			SkillMD string `json:"skill_md" minLength:"10" maxLength:"200000"`
		}
	}) (*struct{ Body SkillView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		sk, _, err := a.Skills.Store(ctx, actorOf(p), in.Space, skills.Files{"SKILL.md": []byte(in.Body.SkillMD)}, "local", 0)
		if err != nil {
			return nil, skillError(err)
		}
		return &struct{ Body SkillView }{Body: a.skillView(ctx, sk)}, nil
	})

	huma.Register(api, op("export-skill", http.MethodGet, "/spaces/{space}/skills/{skill}/export", "Export the current version of a skill as a zip", "skills"), func(ctx context.Context, in *SkillPath) (*struct {
		ContentType        string `header:"Content-Type"`
		ContentDisposition string `header:"Content-Disposition"`
		Body               []byte
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		sk, err := a.Skills.Get(ctx, in.Space, in.Skill)
		if err != nil {
			return nil, skillError(err)
		}
		v, err := a.Skills.Version(ctx, sk, sk.CurrentVersion)
		if err != nil {
			return nil, skillError(err)
		}
		files, err := a.Skills.Files(ctx, sk, v)
		if err != nil {
			return nil, err
		}
		return &struct {
			ContentType        string `header:"Content-Type"`
			ContentDisposition string `header:"Content-Disposition"`
			Body               []byte
		}{ContentType: "application/zip", ContentDisposition: `attachment; filename="` + sk.Name + `.zip"`, Body: files.Zip(sk.Name)}, nil
	})

	huma.Register(api, op("delete-skill", http.MethodDelete, "/spaces/{space}/skills/{skill}", "Delete a skill and its versions", "skills"), func(ctx context.Context, in *SkillPath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, skillError(a.Skills.Delete(ctx, actorOf(p), in.Space, in.Skill))
	})

	huma.Register(api, op("assign-skill", http.MethodPut, "/spaces/{space}/skills/{skill}/bots/{bot}", "Give a bot a skill, or take it away", "skills"), func(ctx context.Context, in *struct {
		SkillPath
		Bot  uuid.UUID `path:"bot"`
		Body struct {
			On bool `json:"on"`
		}
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if _, err := a.Skills.Get(ctx, in.Space, in.Skill); err != nil {
			return nil, skillError(err)
		}
		if _, err := a.Bots.Get(ctx, in.Space, in.Bot); err != nil {
			return nil, apiError(err)
		}
		return nil, a.Skills.Assign(ctx, actorOf(p), in.Space, in.Bot, in.Skill, in.Body.On)
	})

	huma.Register(api, op("sync-skael", http.MethodPost, "/spaces/{space}/skills/sync", "Pull the Skael set now", "skills"), func(ctx context.Context, in *SpacePath) (*struct{ Body skills.SyncResult }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		c, err := a.skaelConfig(ctx, in.Space)
		if err != nil {
			return nil, skillError(err)
		}
		res, err := a.Skills.Sync(ctx, a.Skael, actorOf(p), in.Space, c)
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		return &struct{ Body skills.SyncResult }{Body: res}, nil
	})

	huma.Register(api, op("draft-skill", http.MethodPost, "/spaces/{space}/runs/{run}/draft-skill", "Draft a SKILL.md from a finished run (Save as skill)", "skills"), func(ctx context.Context, in *struct {
		SpacePath
		Run uuid.UUID `path:"run"`
	}) (*struct {
		Body struct {
			SkillMD string `json:"skill_md"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		md, err := a.DraftSkill(ctx, in.Space, in.Run)
		if err != nil {
			return nil, apiError(err)
		}
		out := &struct {
			Body struct {
				SkillMD string `json:"skill_md"`
			}
		}{}
		out.Body.SkillMD = md
		return out, nil
	})
}
