//go:build ee

package ee

import (
	"context"
	"net/http"
	"time"

	"github.com/alternayte/fettle/internal/app"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/ee/licensekey"
)

// SpaceUsage is one space in the admin console's usage report.
type SpaceUsage struct {
	SpaceID string  `json:"space_id"`
	Name    string  `json:"name"`
	Members int     `json:"members"`
	Bots    int     `json:"bots"`
	Runs    int     `json:"runs"`
	Tokens  int64   `json:"tokens"`
	Seconds int64   `json:"computer_seconds"`
	CostUSD float64 `json:"cost_usd"`
}

// UsageReport is the admin console's report across all spaces (§18.3).
type UsageReport struct {
	Days    int          `json:"days"`
	Spaces  []SpaceUsage `json:"spaces"`
	CostUSD float64      `json:"cost_usd"`
	Seats   int          `json:"seats_used" doc:"Active accounts, against the seats of the licence."`
}

func (x *Enterprise) consoleRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "usage-report", Method: http.MethodGet, Path: app.APIPrefix + "/admin/usage-report", Summary: "Usage of every space: members, bots, runs, tokens, computer time and cost (Enterprise admin console)", Tags: []string{"enterprise"}}, func(ctx context.Context, in *struct {
		Days int `query:"days" minimum:"1" maximum:"366" default:"30"`
	}) (*struct{ Body UsageReport }, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if !x.Has(ctx, licensekey.FeatureConsole, false) {
			return nil, huma.Error403Forbidden(ErrFeature.Error())
		}
		rep, err := x.usageReport(ctx, in.Days)
		if err != nil {
			return nil, err
		}
		return &struct{ Body UsageReport }{Body: rep}, nil
	})
}

func (x *Enterprise) usageReport(ctx context.Context, days int) (UsageReport, error) {
	since := time.Now().UTC().AddDate(0, 0, -days)
	rep := UsageReport{Days: days, Spaces: []SpaceUsage{}}
	rows, err := x.a.DB.Engine.Query(ctx, `SELECT s.id, s.name,
		(SELECT COUNT(*) FROM space_members m WHERE m.space_id = s.id),
		(SELECT COUNT(*) FROM bots b WHERE b.space_id = s.id AND b.status = 'active'),
		(SELECT COUNT(*) FROM runs r WHERE r.space_id = s.id AND r.kind = 'owner' AND r.created_at >= $1),
		(SELECT COALESCE(SUM(u.tokens_in + u.tokens_out), 0) FROM usage_events u WHERE u.space_id = s.id AND u.at >= $2),
		(SELECT COALESCE(SUM(CASE WHEN u.unit = 'seconds' THEN u.quantity ELSE 0 END), 0) FROM usage_events u WHERE u.space_id = s.id AND u.at >= $3),
		(SELECT COALESCE(SUM(u.cost_usd), 0) FROM usage_events u WHERE u.space_id = s.id AND u.at >= $4)
		FROM spaces s ORDER BY s.name`, since, since, since, since)
	if err != nil {
		return rep, err
	}
	defer rows.Close()
	for rows.Next() {
		var u SpaceUsage
		var id uuid.UUID
		if err := rows.Scan(&id, &u.Name, &u.Members, &u.Bots, &u.Runs, &u.Tokens, &u.Seconds, &u.CostUSD); err != nil {
			return rep, err
		}
		u.SpaceID = id.String()
		rep.CostUSD += u.CostUSD
		rep.Spaces = append(rep.Spaces, u)
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}
	err = x.a.DB.Engine.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE status = 'active'").Scan(&rep.Seats)
	return rep, err
}
