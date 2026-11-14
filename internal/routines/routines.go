// Package routines runs prompts for bots on cron schedules and signed webhooks (§13.2).
package routines

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/alternayte/fettle/internal/audit"
)

// Errors of the routines service.
var (
	ErrNotFound   = errors.New("routine not found")
	ErrLimit      = errors.New("this bot has the most routines it can have")
	ErrBadCron    = errors.New("the schedule is not a valid cron expression")
	ErrBadTZ      = errors.New("the time zone is not known")
	ErrBadRoutine = errors.New("a routine needs a name, a prompt and a trigger")
	ErrSignature  = errors.New("the webhook signature is missing or wrong")
	ErrDisabled   = errors.New("this routine is paused")
)

// KeepRuns is the number of run records kept per routine in the list (§13.2).
const KeepRuns = 20

// Sealer encrypts the webhook secret with the space data key.
type Sealer interface {
	Seal(ctx context.Context, spaceID uuid.UUID, plain []byte, aad string) ([]byte, error)
	Open(ctx context.Context, spaceID uuid.UUID, sealed []byte, aad string) ([]byte, error)
}

// Firing is one routine firing for the inbox.
type Firing struct {
	Routine *Routine
	DueAt   time.Time
	Source  string // cron, webhook or manual
	Payload string // webhook body, as data
}

// Service manages routines and fires them.
type Service struct {
	e      *drel.Engine
	audit  *audit.Log
	sealer Sealer
	// Fire creates the envelope and the run of a firing in the transaction in ctx and
	// returns the envelope ID. The app sets it to the inbox.
	Fire func(ctx context.Context, f Firing) (uuid.UUID, error)
	// MaxPerBot is the routine limit of a bot (default 50).
	MaxPerBot func(ctx context.Context, spaceID uuid.UUID) int
	now       func() time.Time
	// Started is when this process started; ticks due before it were missed.
	Started time.Time
}

// New returns the routines service.
func New(e *drel.Engine, a *audit.Log, s Sealer) *Service {
	now := func() time.Time { return time.Now().UTC() }
	return &Service{e: e, audit: a, sealer: s, now: now, Started: now()}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// schedule parses a cron expression in a time zone.
func schedule(expr, tz string) (cron.Schedule, *time.Location, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		return nil, nil, ErrBadTZ
	}
	sch, err := parser.Parse(strings.TrimSpace(expr))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrBadCron, err.Error())
	}
	return sch, loc, nil
}

// next returns the first due time strictly after t, in UTC.
func next(sch cron.Schedule, loc *time.Location, t time.Time) time.Time {
	return sch.Next(t.In(loc)).UTC()
}

// Input creates or changes a routine.
type Input struct {
	BotID   uuid.UUID
	Name    string
	Prompt  string
	Trigger string // cron or webhook
	Cron    string
	TZ      string
	Notify  string
	Enabled bool
}

func (in *Input) clean() error {
	in.Name, in.Prompt = strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt)
	if in.Name == "" || in.Prompt == "" || (in.Trigger != "cron" && in.Trigger != "webhook") {
		return ErrBadRoutine
	}
	if in.Notify == "" {
		in.Notify = "on_failure"
	}
	if in.Notify != "always" && in.Notify != "on_failure" && in.Notify != "never" {
		return ErrBadRoutine
	}
	if in.TZ == "" {
		in.TZ = "UTC"
	}
	if in.Trigger == "cron" {
		if _, _, err := schedule(in.Cron, in.TZ); err != nil {
			return err
		}
	}
	return nil
}

// Create adds a routine. A webhook routine returns its secret once.
func (s *Service) Create(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in Input) (*Routine, string, error) {
	if err := in.clean(); err != nil {
		return nil, "", err
	}
	r := &Routine{SpaceID: spaceID, BotID: in.BotID, Name: in.Name, Prompt: in.Prompt, Trigger: in.Trigger, Cron: in.Cron, TZ: in.TZ, Notify: in.Notify, Enabled: in.Enabled, CreatedBy: actor.Kind + ":" + actor.ID}
	secret := ""
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		var n int
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM routines WHERE bot_id = $1", in.BotID).Scan(&n); err != nil {
			return err
		}
		limit := 50
		if s.MaxPerBot != nil {
			if v := s.MaxPerBot(ctx, spaceID); v > 0 {
				limit = v
			}
		}
		if n >= limit {
			return ErrLimit
		}
		repo := drel.Repo(tx, RoutineMeta)
		repo.Add(r)
		if r.Trigger == "webhook" {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			secret = hex.EncodeToString(b)
			sealed, err := s.sealer.Seal(ctx, spaceID, []byte(secret), "routine:"+r.ID().String())
			if err != nil {
				return err
			}
			h := sha256.Sum256([]byte(secret))
			r.WebhookSecret, r.WebhookSecretHash = base64.StdEncoding.EncodeToString(sealed), hex.EncodeToString(h[:])
		} else {
			sch, loc, _ := schedule(r.Cron, r.TZ)
			n := next(sch, loc, s.now())
			r.NextAt = &n
		}
		return s.audit.Record(ctx, actor, &spaceID, "routine.create", "routine:"+r.ID().String(), map[string]string{"name": r.Name, "trigger": r.Trigger, "bot": r.BotID.String()})
	})
	if err != nil {
		return nil, "", err
	}
	return r, secret, nil
}

// Update changes a routine. A cron change moves the next due time.
func (s *Service) Update(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, in Input) (*Routine, error) {
	if err := in.clean(); err != nil {
		return nil, err
	}
	var out *Routine
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		r, err := drel.Repo(drel.MustFromContext(ctx), RoutineMeta).Where(Routines.ID.Eq(id)).Where(Routines.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if r == nil {
			return ErrNotFound
		}
		if in.Trigger != r.Trigger {
			return fmt.Errorf("%w: the trigger of a routine does not change; make a new routine", ErrBadRoutine)
		}
		r.Name, r.Prompt, r.Notify, r.Enabled = in.Name, in.Prompt, in.Notify, in.Enabled
		if r.Trigger == "cron" && (r.Cron != in.Cron || r.TZ != in.TZ || r.NextAt == nil) {
			r.Cron, r.TZ = in.Cron, in.TZ
			sch, loc, _ := schedule(r.Cron, r.TZ)
			n := next(sch, loc, s.now())
			r.NextAt = &n
		}
		out = r
		return s.audit.Record(ctx, actor, &spaceID, "routine.update", "routine:"+id.String(), map[string]string{"name": r.Name, "enabled": fmt.Sprint(r.Enabled)})
	})
	return out, err
}

// SetEnabled pauses or resumes a routine (archive and duplicate pause them).
func (s *Service) SetEnabled(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, on bool) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		n, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE routines SET enabled = $1, updated_at = $2 WHERE id = $3 AND space_id = $4", on, s.now(), id, spaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, actor, &spaceID, "routine.enable", "routine:"+id.String(), map[string]string{"enabled": fmt.Sprint(on)})
	})
}

// Delete removes a routine and its run records.
func (s *Service) Delete(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		n, err := drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM routines WHERE id = $1 AND space_id = $2", id, spaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, actor, &spaceID, "routine.delete", "routine:"+id.String(), nil)
	})
}

// Get returns a routine of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Routine, error) {
	r, err := drel.NewRepository(s.e, RoutineMeta).Where(Routines.ID.Eq(id)).Where(Routines.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrNotFound
	}
	return r, nil
}

// List returns the routines of a space, or of one bot.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID) ([]*Routine, error) {
	q := drel.NewRepository(s.e, RoutineMeta).Where(Routines.SpaceID.Eq(spaceID))
	if botID != nil {
		q = q.Where(Routines.BotID.Eq(*botID))
	}
	return q.OrderBy(Routines.Name.Asc()).All(ctx)
}

// Runs returns the newest run records of a routine.
func (s *Service) Runs(ctx context.Context, routineID uuid.UUID) ([]*RoutineRun, error) {
	return drel.NewRepository(s.e, RoutineRunMeta).Where(RoutineRuns.RoutineID.Eq(routineID)).OrderBy(RoutineRuns.DueAt.Desc()).Limit(KeepRuns).All(ctx)
}

// Tick fires every cron routine that is due. Ticks missed while Fettle did not run become
// one firing with the first due time; the others are recorded as coalesced (§7.5, §13.2).
// The idempotency key routine:due-time makes a second process's tick fail, not repeat.
func (s *Service) Tick(ctx context.Context) (int, error) {
	now := s.now()
	due, err := drel.NewRepository(s.e, RoutineMeta).Where(Routines.Enabled.Eq(true)).Where(Routines.Trigger.Eq("cron")).Where(Routines.NextAt.LTE(&now)).All(ctx)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, r := range due {
		sch, loc, err := schedule(r.Cron, r.TZ)
		if err != nil {
			continue
		}
		var ticks []time.Time
		for t := *r.NextAt; !t.After(now) && len(ticks) < 10000; t = next(sch, loc, t) {
			ticks = append(ticks, t)
		}
		nextAt := next(sch, loc, now)
		err = s.e.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			n, err := tx.Exec(ctx, "UPDATE routines SET next_at = $1, updated_at = $2 WHERE id = $3 AND next_at = $4", nextAt, now, r.ID(), *r.NextAt)
			if err != nil || n == 0 {
				return err // another process took this tick
			}
			envID, err := s.Fire(ctx, Firing{Routine: r, DueAt: ticks[0], Source: "cron"})
			if err != nil {
				return err
			}
			repo := drel.Repo(tx, RoutineRunMeta)
			started := now
			repo.Add(&RoutineRun{RoutineID: r.ID(), EnvelopeID: &envID, DueAt: ticks[0], StartedAt: &started, Missed: ticks[0].Before(s.Started), Source: "cron"})
			for _, t := range ticks[1:] {
				repo.Add(&RoutineRun{RoutineID: r.ID(), DueAt: t, Coalesced: true, Missed: t.Before(s.Started), Source: "cron"})
			}
			if err := tx.SaveChanges(ctx); err != nil {
				return err
			}
			return s.prune(ctx, r.ID())
		})
		if err != nil {
			return fired, fmt.Errorf("fire routine %s: %w", r.ID(), err)
		}
		fired++
	}
	return fired, nil
}

// prune keeps the newest run records of a routine.
func (s *Service) prune(ctx context.Context, routineID uuid.UUID) error {
	tx := drel.MustFromContext(ctx)
	_, err := tx.Exec(ctx, `DELETE FROM routine_runs WHERE routine_id = $1 AND id NOT IN (
		SELECT id FROM routine_runs WHERE routine_id = $2 ORDER BY due_at DESC LIMIT 20)`, routineID, routineID)
	return err
}

// FireNow fires a routine at once (the Run now button).
func (s *Service) FireNow(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) (uuid.UUID, error) {
	r, err := s.Get(ctx, spaceID, id)
	if err != nil {
		return uuid.Nil, err
	}
	return s.fireOne(ctx, r, "manual", "", func(ctx context.Context) error {
		return s.audit.Record(ctx, actor, &spaceID, "routine.run", "routine:"+id.String(), nil)
	})
}

func (s *Service) fireOne(ctx context.Context, r *Routine, source, payload string, also func(ctx context.Context) error) (uuid.UUID, error) {
	now := s.now()
	var envID uuid.UUID
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		var err error
		envID, err = s.Fire(ctx, Firing{Routine: r, DueAt: now, Source: source, Payload: payload})
		if err != nil {
			return err
		}
		drel.Repo(drel.MustFromContext(ctx), RoutineRunMeta).Add(&RoutineRun{RoutineID: r.ID(), EnvelopeID: &envID, DueAt: now, StartedAt: &now, Source: source})
		if err := drel.MustFromContext(ctx).SaveChanges(ctx); err != nil {
			return err
		}
		if also != nil {
			if err := also(ctx); err != nil {
				return err
			}
		}
		return s.prune(ctx, r.ID())
	})
	return envID, err
}

// Verify checks a webhook request before its body is parsed (SI-17): an HMAC-SHA256 of the
// body in X-Fettle-Signature ("sha256=<hex>"), or the secret in X-Fettle-Secret.
func (s *Service) Verify(ctx context.Context, id uuid.UUID, signature, secretHeader string, body []byte) (*Routine, error) {
	r, err := drel.NewRepository(s.e, RoutineMeta).Where(Routines.ID.Eq(id)).Where(Routines.Trigger.Eq("webhook")).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrSignature // the same answer as a bad signature, so IDs do not leak
	}
	if secretHeader != "" {
		h := sha256.Sum256([]byte(secretHeader))
		if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(r.WebhookSecretHash)) == 1 {
			return r, nil
		}
		return nil, ErrSignature
	}
	sig, ok := strings.CutPrefix(strings.TrimSpace(signature), "sha256=")
	if !ok {
		return nil, ErrSignature
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return nil, ErrSignature
	}
	sealed, err := base64.StdEncoding.DecodeString(r.WebhookSecret)
	if err != nil {
		return nil, err
	}
	secret, err := s.sealer.Open(ctx, r.SpaceID, sealed, "routine:"+r.ID().String())
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), got) {
		return nil, ErrSignature
	}
	return r, nil
}

// FireWebhook fires a verified webhook routine with its body as data.
func (s *Service) FireWebhook(ctx context.Context, r *Routine, payload string) (uuid.UUID, error) {
	if !r.Enabled {
		return uuid.Nil, ErrDisabled
	}
	return s.fireOne(ctx, r, "webhook", payload, nil)
}

// MarkSendApproved records that a person allowed the first external send of a routine.
func (s *Service) MarkSendApproved(ctx context.Context, id uuid.UUID) error {
	_, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE routines SET send_approved = $1 WHERE id = $2", true, id)
	return err
}

// FirstSend reports whether a routine has had no allowed external send yet.
func (s *Service) FirstSend(ctx context.Context, id uuid.UUID) bool {
	var ok bool
	if err := s.e.QueryRow(ctx, "SELECT send_approved FROM routines WHERE id = $1", id).Scan(&ok); err != nil {
		return false
	}
	return !ok
}
