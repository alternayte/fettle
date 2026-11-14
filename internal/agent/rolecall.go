package agent

import (
	"context"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/usage"
)

// CallRole makes one model call with a role of a bot outside the owner turn (for example
// computer_mode), and records its usage and cost on the run (REQ-MOD-007).
func (a *Agent) CallRole(ctx context.Context, env *ToolEnv, role string, req models.Request) (models.Response, error) {
	targets, err := a.Models.Resolve(ctx, env.SpaceID, &env.BotID, role)
	if err != nil {
		return models.Response{}, err
	}
	step := ""
	_ = a.E.QueryRow(ctx, "SELECT COALESCE(e.step, '') FROM runs r JOIN envelopes e ON e.id = r.envelope_id WHERE r.id = $1", env.RunID).Scan(&step)
	return a.callTargets(ctx, env.SpaceID, env.BotID, env.RunID, step, role, targets, req)
}

func (a *Agent) callTargets(ctx context.Context, spaceID, botID, runID uuid.UUID, step, role string, targets []models.Resolved, req models.Request) (models.Response, error) {
	resp, idx, err := models.CallWithFallback(ctx, a.HTTP, targets, req, nil, nil)
	if err != nil {
		return resp, err
	}
	cost, reported := models.Cost(resp.Usage, targets[idx].Target)
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE runs SET tokens_in = tokens_in + $1, tokens_out = tokens_out + $2, cost_usd = cost_usd + $3 WHERE id = $4", resp.Usage.In, resp.Usage.Out, cost, runID); err != nil {
			return err
		}
		return a.Usage.Record(ctx, &usage.UsageEvent{SpaceID: spaceID, Kind: "model", BotID: &botID, RunID: &runID, Step: step, Role: role, ModelName: resp.Model, TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, CacheRead: resp.Usage.CacheRead, Quantity: resp.Usage.In + resp.Usage.Out, Unit: "tokens", CostUSD: cost, Reported: reported})
	})
	if a.Metrics != nil {
		a.Metrics.ModelCall(role, resp.Model, resp.Usage.In, resp.Usage.Out, cost)
	}
	return resp, err
}
