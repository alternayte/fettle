// Package usage records model tokens, cost and computer seconds, and reports cost views (§8.4).
package usage

import (
	"context"
	"fmt"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Service records usage and reads cost views.
type Service struct {
	e  *drel.Engine
	pg bool
}

// New returns the usage service.
func New(e *drel.Engine) *Service { return &Service{e: e, pg: e.DialectName() == "postgres"} }

// Record writes one usage row in the transaction in ctx.
func (s *Service) Record(ctx context.Context, ev *UsageEvent) error {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), UsageEventMeta).Add(ev)
		return nil
	})
}

// Dimensions of the cost views.
var Dimensions = map[string]string{
	"run":   "run_id",
	"bot":   "bot_id",
	"step":  "step",
	"role":  "role",
	"model": "model",
	"day":   "day",
	"month": "month",
}

// Row is one group of a cost view.
type Row struct {
	Key       string  `json:"key"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	Seconds   int64   `json:"seconds"`
	CostUSD   float64 `json:"cost_usd"`
	Calls     int64   `json:"calls"`
}

// View groups the usage of a space by one dimension since a time.
func (s *Service) View(ctx context.Context, spaceID uuid.UUID, dim string, since time.Time) ([]Row, error) {
	col, ok := Dimensions[dim]
	if !ok {
		return nil, fmt.Errorf("unknown cost dimension %q", dim)
	}
	expr := "COALESCE(CAST(" + col + " AS TEXT), '')"
	switch dim {
	case "day":
		expr = "to_char(at, 'YYYY-MM-DD')"
		if !s.pg {
			expr = "substr(at, 1, 10)"
		}
	case "month":
		expr = "to_char(at, 'YYYY-MM')"
		if !s.pg {
			expr = "substr(at, 1, 7)"
		}
	}
	q := `SELECT ` + expr + ` AS k, COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0),
		COALESCE(SUM(CASE WHEN unit = 'seconds' THEN quantity ELSE 0 END),0), COALESCE(SUM(cost_usd),0), COUNT(*)
		FROM usage_events WHERE space_id = $1 AND at >= $2 GROUP BY k ORDER BY k`
	rows, err := s.e.Query(ctx, q, spaceID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Key, &r.TokensIn, &r.TokensOut, &r.Seconds, &r.CostUSD, &r.Calls); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Spent returns the USD spent by a bot since a time, or by the space when botID is nil.
func (s *Service) Spent(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID, since time.Time) (float64, error) {
	var v float64
	var err error
	if botID != nil {
		err = s.e.QueryRow(ctx, "SELECT COALESCE(SUM(cost_usd),0) FROM usage_events WHERE space_id = $1 AND bot_id = $2 AND at >= $3", spaceID, *botID, since).Scan(&v)
	} else {
		err = s.e.QueryRow(ctx, "SELECT COALESCE(SUM(cost_usd),0) FROM usage_events WHERE space_id = $1 AND at >= $2", spaceID, since).Scan(&v)
	}
	return v, err
}
