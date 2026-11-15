package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/skills"
)

// runEvals runs the evals.json of one skill version with one bot's profile (§13.1). Without
// a bot it uses a bot that has the skill, else the primary bot. version 0 is the current one.
func (a *App) runEvals(ctx context.Context, spaceID, skillID uuid.UUID, botID *uuid.UUID, version, trials int) (skills.EvalReport, error) {
	sk, err := a.Skills.Get(ctx, spaceID, skillID)
	if err != nil {
		return skills.EvalReport{}, err
	}
	if version == 0 {
		version = sk.CurrentVersion
	}
	v, err := a.Skills.Version(ctx, sk, version)
	if err != nil {
		return skills.EvalReport{}, err
	}
	files, err := a.Skills.Files(ctx, sk, v)
	if err != nil {
		return skills.EvalReport{}, err
	}
	ev, err := skills.ParseEvals(files["evals.json"])
	if err != nil {
		return skills.EvalReport{}, err
	}
	var bot uuid.UUID
	if botID != nil {
		b, err := a.Bots.Get(ctx, spaceID, *botID)
		if err != nil {
			return skills.EvalReport{}, err
		}
		bot = b.ID()
	} else if err := a.DB.Engine.QueryRow(ctx, "SELECT bs.bot_id FROM bot_skills bs JOIN bots b ON b.id = bs.bot_id WHERE bs.skill_id = $1 AND b.status = 'active' ORDER BY b.created_at LIMIT 1", skillID).Scan(&bot); err != nil {
		p, err := a.Bots.Primary(ctx, spaceID)
		if err != nil {
			return skills.EvalReport{}, err
		}
		bot = p.ID()
	}
	text := fmt.Sprintf("Skill %s, version %d:\n%s", sk.Name, v.Version, files["SKILL.md"])
	return a.Agent.EvalSkill(ctx, agent.EvalInput{SpaceID: spaceID, BotID: bot, Skill: sk.Name, Version: v.Version, SkillText: text, Evals: ev, Trials: trials})
}

func (a *App) registerEvals(api huma.API) {
	huma.Register(api, op("run-skill-evals", http.MethodPost, "/spaces/{space}/skills/{skill}/evals", "Run the evals.json of a skill version, graded by the verifier role", "skills"), func(ctx context.Context, in *struct {
		SkillPath
		Body struct {
			BotID   *uuid.UUID `json:"bot_id,omitempty" doc:"The bot whose profile runs the cases. Default: a bot with the skill, else the primary bot."`
			Version int        `json:"version,omitempty" doc:"The skill version. Default: the current version."`
			Trials  int        `json:"trials,omitempty" minimum:"0" maximum:"5" doc:"Trials per case. Default 1."`
		}
	}) (*struct{ Body skills.EvalReport }, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		rep, err := a.runEvals(ctx, in.Space, in.Skill, in.Body.BotID, in.Body.Version, in.Body.Trials)
		if errors.Is(err, skills.ErrNoEvals) {
			return nil, huma.Error409Conflict("this skill version has no evals.json")
		}
		switch {
		case errors.Is(err, skills.ErrBadEvals):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		case errors.Is(err, bots.ErrNotFound):
			return nil, apiError(err)
		case err != nil:
			return nil, skillError(err)
		}
		return &struct{ Body skills.EvalReport }{Body: rep}, nil
	})
}
