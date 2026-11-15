package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/skills"
	"github.com/alternayte/fettle/internal/usage"
)

// EvalInput is what an eval of one skill version runs with: the bot's profile and the
// skill text as a run loads it.
type EvalInput struct {
	SpaceID   uuid.UUID
	BotID     uuid.UUID
	Skill     string
	Version   int
	SkillText string
	Evals     skills.Evals
	Trials    int
}

// EvalSkill runs the eval cases of a skill version (§13.1, §14.3). Each trial is one turn
// of the owner role with the skill loaded and the bot's tools offered but not run; the
// output is the reply and its tool calls. The verifier role grades each expectation.
func (a *Agent) EvalSkill(ctx context.Context, in EvalInput) (skills.EvalReport, error) {
	rep := skills.EvalReport{Skill: in.Skill, Version: in.Version, Cases: []skills.EvalResult{}}
	if in.Trials <= 0 {
		in.Trials = 1
	}
	b, err := a.Bots.Get(ctx, in.SpaceID, in.BotID)
	if err != nil {
		return rep, err
	}
	targets, err := a.Models.Resolve(ctx, in.SpaceID, &in.BotID, models.RoleOwner)
	if err != nil {
		return rep, err
	}
	tz := "UTC"
	if a.Spaces != nil {
		if sp, err := a.Spaces.Get(ctx, in.SpaceID); err == nil {
			tz = sp.TimeZone
		}
	}
	tools := toolDefs(a.Tools.For(UserCapabilities, false))
	var total float64
	rep.Pass = true
	for _, c := range in.Evals.Evals {
		res := skills.EvalResult{ID: c.ID, Prompt: c.Prompt, Trials: in.Trials}
		met := make([]int, len(c.Expectations))
		reasons := make([]string, len(c.Expectations))
		var score float64
		for trial := 0; trial < in.Trials; trial++ {
			ctxt := Assemble(Input{BotName: b.Name, BotJob: b.Job, Instructions: b.Instructions, LoadedSkill: in.SkillText,
				Now: a.now(), TimeZone: tz, Goal: c.Prompt, GoalOrigin: OriginUser, Capabilities: UserCapabilities, ContextWindow: 128000})
			req := models.Request{System: ctxt.System, Messages: ctxt.Messages, Tools: tools, MaxTokens: 4096}
			resp, idx, err := models.CallWithFallback(ctx, a.HTTP, targets, req, nil, nil)
			if err != nil {
				res.Error = err.Error()
				break
			}
			cost, reported := models.Cost(resp.Usage, targets[idx].Target)
			rep.CostUSD += cost
			_ = a.Usage.Record(ctx, &usage.UsageEvent{SpaceID: in.SpaceID, Kind: "model", BotID: &in.BotID, Step: "eval", Role: models.RoleOwner, ModelName: resp.Model, TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, Quantity: resp.Usage.In + resp.Usage.Out, Unit: "tokens", CostUSD: cost, Reported: reported})
			out := evalOutput(&resp)
			res.Output = out
			all := true
			var n int
			for i, x := range c.Expectations {
				ok, reason, err := a.Grade(ctx, in.SpaceID, in.BotID, c.Prompt, x, out)
				if err != nil {
					res.Error = err.Error()
					ok = false
				}
				if ok {
					met[i]++
					n++
				} else {
					all = false
				}
				reasons[i] = reason
			}
			if len(c.Expectations) == 0 {
				score++
			} else {
				score += float64(n) / float64(len(c.Expectations))
			}
			if all {
				res.Passed++
			}
		}
		for i, x := range c.Expectations {
			res.Expectations = append(res.Expectations, skills.Expectation{Text: x, Pass: met[i]*2 >= in.Trials, Reason: reasons[i]})
		}
		res.Score = score / float64(in.Trials)
		res.Pass = res.Error == "" && res.Passed*2 >= in.Trials
		if !res.Pass {
			rep.Pass = false
		}
		total += res.Score
		rep.Cases = append(rep.Cases, res)
	}
	if len(rep.Cases) > 0 {
		rep.Score = total / float64(len(rep.Cases))
	}
	return rep, nil
}

// evalOutput is the reply text with its tool calls, which is what the grader sees.
func evalOutput(resp *models.Response) string {
	var b strings.Builder
	b.WriteString(resp.Text)
	for _, c := range resp.ToolCalls {
		fmt.Fprintf(&b, "\n\nTool call %s: %s", c.Name, c.Args)
	}
	return strings.TrimSpace(b.String())
}
