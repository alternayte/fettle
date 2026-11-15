package app

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/auth"
)

// usageLine is one usage event in the export.
type usageLine struct {
	ID        string     `json:"id"`
	SpaceID   string     `json:"space_id"`
	Kind      string     `json:"kind"`
	BotID     *uuid.UUID `json:"bot_id,omitempty"`
	ZoneID    *uuid.UUID `json:"zone_id,omitempty"`
	RunID     *uuid.UUID `json:"run_id,omitempty"`
	Step      string     `json:"step,omitempty"`
	Role      string     `json:"role,omitempty"`
	Model     string     `json:"model,omitempty"`
	TokensIn  int64      `json:"tokens_in"`
	TokensOut int64      `json:"tokens_out"`
	CacheRead int64      `json:"cache_read"`
	Quantity  int64      `json:"quantity"`
	Unit      string     `json:"unit"`
	Size      string     `json:"size,omitempty"`
	CostUSD   float64    `json:"cost_usd"`
	Reported  bool       `json:"reported"`
	At        time.Time  `json:"at"`
}

// usageExport streams usage_events as NDJSON for instance admins (§18.4): one JSON object
// per line, oldest first. Query: since and until (RFC 3339), space (an ID).
func (a *App) usageExport(w http.ResponseWriter, r *http.Request) {
	p := auth.From(r.Context())
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Sign in first."})
		return
	}
	if !p.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Only an instance admin can export usage."})
		return
	}
	q := r.URL.Query()
	since, until := time.Unix(0, 0).UTC(), time.Now().UTC().Add(time.Second)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "since is an RFC 3339 time."})
			return
		}
		since = t.UTC()
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "until is an RFC 3339 time."})
			return
		}
		until = t.UTC()
	}
	var space *uuid.UUID
	if v := q.Get("space"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "space is a space ID."})
			return
		}
		space = &id
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	fl, _ := w.(http.Flusher)
	// Pages of 1000 rows in the order (at, id), after the last row of the page before, so the
	// export streams any size.
	after, afterID := since, uuid.Nil.String()
	for {
		q := `SELECT id, space_id, kind, bot_id, zone_id, run_id, step, role, model, tokens_in, tokens_out, cache_read, quantity, unit, size, cost_usd, reported, at
			FROM usage_events WHERE at < $1 AND (at > $2 OR (at = $3 AND id > $4))`
		args := []any{until, after, after, afterID}
		if space != nil {
			q += " AND space_id = $5"
			args = append(args, *space)
		}
		q += " ORDER BY at, id LIMIT 1000"
		rows, err := a.DB.Engine.Query(r.Context(), q, args...)
		if err != nil {
			a.Log.Error("usage export", "error", err)
			return
		}
		n := 0
		for rows.Next() {
			var l usageLine
			var id, sp uuid.UUID
			if err := rows.Scan(&id, &sp, &l.Kind, &l.BotID, &l.ZoneID, &l.RunID, &l.Step, &l.Role, &l.Model, &l.TokensIn, &l.TokensOut, &l.CacheRead, &l.Quantity, &l.Unit, &l.Size, &l.CostUSD, &l.Reported, &l.At); err != nil {
				rows.Close()
				a.Log.Error("usage export", "error", err)
				return
			}
			l.ID, l.SpaceID, l.At = id.String(), sp.String(), l.At.UTC()
			if err := enc.Encode(l); err != nil {
				rows.Close()
				return
			}
			after, afterID = l.At, l.ID
			n++
		}
		rows.Close()
		if fl != nil {
			fl.Flush()
		}
		if n < 1000 {
			return
		}
	}
}
