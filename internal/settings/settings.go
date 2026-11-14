// Package settings keeps versioned instance and space settings with history (REQ-CORE-009).
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
)

// Def describes one known setting.
type Def struct {
	Key     string
	Scope   string // instance or space
	Default any
	Doc     string
}

// Known settings and their defaults.
var defs = map[string]Def{}

// Register adds a setting definition. Feature packages call it from init.
func Register(d Def) {
	if _, dup := defs[d.Key]; dup {
		panic("settings: duplicate key " + d.Key)
	}
	defs[d.Key] = d
}

// Defs returns all definitions sorted by key.
func Defs() []Def {
	out := make([]Def, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func init() {
	Register(Def{Key: "agent.max_turns", Scope: "space", Default: 200, Doc: "Model turns before a run stops and asks."})
	Register(Def{Key: "bots.max_per_space", Scope: "space", Default: 50, Doc: "Bots in one space."})
	Register(Def{Key: "budget.envelope_usd", Scope: "space", Default: 2.0, Doc: "Default USD budget of one envelope."})
	Register(Def{Key: "budget.envelope_tokens", Scope: "space", Default: 2000000, Doc: "Default token budget of one envelope."})
	Register(Def{Key: "budget.envelope_wall_ms", Scope: "space", Default: 3600000, Doc: "Default wall time of one envelope."})
	Register(Def{Key: "budget.hops", Scope: "space", Default: 4, Doc: "Default handoff hops."})
	Register(Def{Key: "budget.max_subagents", Scope: "space", Default: 5, Doc: "Default sub-agents per envelope."})
	Register(Def{Key: "budget.bot_day_usd", Scope: "space", Default: 10.0, Doc: "USD per bot per day."})
	Register(Def{Key: "budget.space_month_usd", Scope: "space", Default: 100.0, Doc: "USD per space per month."})
	Register(Def{Key: "chat.attachment_max_mb", Scope: "space", Default: 50, Doc: "Largest attachment."})
	Register(Def{Key: "space.time_zone", Scope: "space", Default: "UTC", Doc: "Time zone of the space."})
	Register(Def{Key: "space.name", Scope: "space", Default: "", Doc: "Name of the owner."})
	Register(Def{Key: "telemetry.enabled", Scope: "instance", Default: false, Doc: "Send anonymous counts."})
}

// ErrUnknown means the key is not registered.
var ErrUnknown = errors.New("unknown setting")

// Store reads and writes settings.
type Store struct {
	e     *drel.Engine
	audit *audit.Log
}

// New returns the settings store.
func New(e *drel.Engine, a *audit.Log) *Store { return &Store{e: e, audit: a} }

func spaceKey(spaceID *uuid.UUID) string {
	if spaceID == nil {
		return ""
	}
	return spaceID.String()
}

// Value is a setting with its current revision.
type Value struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Revision  int             `json:"revision"`
	IsDefault bool            `json:"is_default"`
}

// Get returns the value of key, or its default.
func (s *Store) Get(ctx context.Context, spaceID *uuid.UUID, key string) (Value, error) {
	d, ok := defs[key]
	if !ok {
		return Value{}, fmt.Errorf("%w: %s", ErrUnknown, key)
	}
	scope, sk := d.Scope, spaceKey(spaceID)
	if scope == "instance" {
		sk = ""
	}
	row, err := drel.NewRepository(s.e, SettingMeta).Where(Settings.Scope.Eq(scope)).Where(Settings.SpaceKey.Eq(sk)).Where(Settings.Key.Eq(key)).FirstOrNil(ctx)
	if err != nil {
		return Value{}, err
	}
	if row == nil {
		b, _ := json.Marshal(d.Default)
		return Value{Key: key, Value: b, Revision: 0, IsDefault: true}, nil
	}
	return Value{Key: key, Value: json.RawMessage(row.Value), Revision: row.Revision}, nil
}

// Int returns an integer setting.
func (s *Store) Int(ctx context.Context, spaceID *uuid.UUID, key string) int {
	v, err := s.Get(ctx, spaceID, key)
	if err != nil {
		return 0
	}
	var n float64
	if json.Unmarshal(v.Value, &n) != nil {
		return 0
	}
	return int(n)
}

// Float returns a number setting.
func (s *Store) Float(ctx context.Context, spaceID *uuid.UUID, key string) float64 {
	v, err := s.Get(ctx, spaceID, key)
	if err != nil {
		return 0
	}
	var n float64
	_ = json.Unmarshal(v.Value, &n)
	return n
}

// Bool returns a boolean setting.
func (s *Store) Bool(ctx context.Context, spaceID *uuid.UUID, key string) bool {
	v, err := s.Get(ctx, spaceID, key)
	if err != nil {
		return false
	}
	var b bool
	_ = json.Unmarshal(v.Value, &b)
	return b
}

// String returns a text setting.
func (s *Store) String(ctx context.Context, spaceID *uuid.UUID, key string) string {
	v, err := s.Get(ctx, spaceID, key)
	if err != nil {
		return ""
	}
	var str string
	_ = json.Unmarshal(v.Value, &str)
	return str
}

// Set changes a setting. The first write has revision 1 when the setting had its
// default; each later write adds one, and history keeps author, time, old and new value.
func (s *Store) Set(ctx context.Context, actor audit.Actor, spaceID *uuid.UUID, key string, value any) (Value, error) {
	d, ok := defs[key]
	if !ok {
		return Value{}, fmt.Errorf("%w: %s", ErrUnknown, key)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Value{}, err
	}
	if err := checkType(d.Default, raw); err != nil {
		return Value{}, fmt.Errorf("%s: %w", key, err)
	}
	scope, sk := d.Scope, spaceKey(spaceID)
	sp := spaceID
	if scope == "instance" {
		sk, sp = "", nil
	} else if spaceID == nil {
		return Value{}, fmt.Errorf("%s is a space setting", key)
	}
	var by *uuid.UUID
	if u, err := uuid.Parse(actor.ID); err == nil && actor.Kind == "user" {
		by = &u
	}
	var out Value
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		repo := drel.Repo(tx, SettingMeta)
		row, err := repo.Where(Settings.Scope.Eq(scope)).Where(Settings.SpaceKey.Eq(sk)).Where(Settings.Key.Eq(key)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		var old *string
		if row == nil {
			def, _ := json.Marshal(d.Default)
			ds := string(def)
			old = &ds
			// The default counts as revision 1, so the first change is revision 2.
			row = &Setting{Scope: scope, SpaceID: sp, SpaceKey: sk, Key: key, Value: string(raw), Revision: 2, UpdatedBy: by}
			repo.Add(row)
		} else {
			o := row.Value
			old = &o
			row.Value, row.Revision, row.UpdatedBy = string(raw), row.Revision+1, by
		}
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		drel.Repo(tx, SettingHistoryMeta).Add(&SettingHistory{SettingID: row.ID(), Revision: row.Revision, OldValue: old, NewValue: string(raw), UpdatedBy: by})
		out = Value{Key: key, Value: raw, Revision: row.Revision}
		return s.audit.Record(ctx, actor, sp, "settings.change", "setting:"+key, map[string]string{"old": *old, "new": string(raw)})
	})
	return out, err
}

// HistoryRow is one change of a setting.
type HistoryRow struct {
	Revision  int        `json:"revision"`
	OldValue  *string    `json:"old_value"`
	NewValue  string     `json:"new_value"`
	UpdatedBy *uuid.UUID `json:"updated_by"`
	At        string     `json:"at"`
}

// History returns the changes of a setting, oldest first.
func (s *Store) History(ctx context.Context, spaceID *uuid.UUID, key string) ([]HistoryRow, error) {
	d, ok := defs[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknown, key)
	}
	sk := spaceKey(spaceID)
	if d.Scope == "instance" {
		sk = ""
	}
	row, err := drel.NewRepository(s.e, SettingMeta).Where(Settings.Scope.Eq(d.Scope)).Where(Settings.SpaceKey.Eq(sk)).Where(Settings.Key.Eq(key)).FirstOrNil(ctx)
	if err != nil || row == nil {
		return nil, err
	}
	hs, err := drel.NewRepository(s.e, SettingHistoryMeta).Where(SettingHistories.SettingID.Eq(row.ID())).OrderBy(SettingHistories.Revision.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryRow, 0, len(hs))
	for _, h := range hs {
		out = append(out, HistoryRow{Revision: h.Revision, OldValue: h.OldValue, NewValue: h.NewValue, UpdatedBy: h.UpdatedBy, At: h.CreatedAt().UTC().Format("2006-01-02T15:04:05.000Z")})
	}
	return out, nil
}

func checkType(def any, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	switch def.(type) {
	case int, float64:
		if _, ok := v.(float64); !ok {
			return errors.New("value must be a number")
		}
	case bool:
		if _, ok := v.(bool); !ok {
			return errors.New("value must be true or false")
		}
	case string:
		if _, ok := v.(string); !ok {
			return errors.New("value must be text")
		}
	}
	return nil
}
