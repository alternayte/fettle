// Package envelopes holds the envelope model, its validation and its lifecycle (Appendix C).
package envelopes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Capability classes (§11.1).
const (
	ClassRead          = "read"
	ClassWriteLocal    = "write_local"
	ClassWriteExternal = "write_external"
	ClassSend          = "send"
	ClassSpend         = "spend"
	ClassAdmin         = "admin"
)

// AllClasses lists the capability classes.
var AllClasses = []string{ClassRead, ClassWriteLocal, ClassWriteExternal, ClassSend, ClassSpend, ClassAdmin}

// Kinds, origins and statuses.
var (
	Kinds    = []string{"message", "routine", "webhook", "handoff", "part", "verify"}
	Origins  = []string{"user", "routine", "webhook", "bot"}
	Statuses = []string{"created", "routed", "running", "verifying", "done", "failed", "escalated", "cancelled"}
	Steps    = []string{"direct", "single", "fanout", "forked", "handoff", "hire"}
	Checks   = []string{"llm", "file", "command", "citations", "human"}
)

// DefaultCheck is the acceptance check when none is given (§9.5).
var DefaultCheck = Check{Kind: "llm", Spec: "The result answers the goal. Each factual claim has evidence."}

// ValidationError lists what is wrong with an envelope.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid envelope: " + strings.Join(e.Problems, "; ")
}

// Remainder is what is left of a parent's budget.
type Remainder struct {
	Budget       Budget
	Capabilities []string
	SpaceID      uuid.UUID
}

// Validate checks an envelope against Appendix C. parent is nil for a root.
func Validate(e *Envelope, parent *Remainder) error {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	if !slices.Contains(Kinds, e.Kind) {
		add("kind %q is not known", e.Kind)
	}
	if !slices.Contains(Origins, e.Origin) {
		add("origin %q is not known", e.Origin)
	}
	if !slices.Contains(Statuses, e.Status) {
		add("status %q is not known", e.Status)
	}
	if n := utf8.RuneCountInString(e.Goal); n < 1 || n > 8000 {
		add("goal must have 1 to 8,000 characters")
	}
	if len(e.Acceptance) == 0 {
		add("acceptance needs at least one check")
	}
	for _, c := range e.Acceptance {
		if !slices.Contains(Checks, c.Kind) {
			add("acceptance check kind %q is not known", c.Kind)
		}
	}
	for _, c := range e.Capabilities {
		if !slices.Contains(AllClasses, c) {
			add("capability %q is not known", c)
		}
	}
	for _, r := range e.ContextRefs {
		if !slices.Contains([]string{"message", "file", "artifact", "run"}, r.Type) {
			add("context ref type %q is not known", r.Type)
		}
		if r.Type == "file" && !strings.HasPrefix(strings.TrimPrefix(r.Ref, "/home/fettle/"), "shared/") {
			add("file context refs must be in shared/")
		}
	}
	if e.Step != nil && !slices.Contains(Steps, *e.Step) {
		add("step %q is not known", *e.Step)
	}
	if (e.Kind == "part" || e.Kind == "verify") && e.Step == nil {
		add("a %s envelope needs a fixed step", e.Kind)
	}
	if e.Budget.USD < 0 || e.Budget.Tokens < 0 || e.Budget.WallMS < 0 || e.Budget.HopsRemaining < 0 || e.Budget.MaxSubagents < 0 {
		add("budget values cannot be negative")
	}
	if e.Budget.MaxSubagents > 8 {
		add("max_subagents is at most 8")
	}
	if strings.TrimSpace(e.IdempotencyKey) == "" {
		add("idempotency_key is required")
	}
	if parent == nil {
		if e.ParentID != nil {
			add("a root has no parent")
		}
		if e.RootID != e.ID() {
			add("root_id must equal id for a root")
		}
	} else {
		if e.ParentID == nil {
			add("a child needs parent_id")
		}
		if e.SpaceID != parent.SpaceID {
			add("a child must be in the space of its parent")
		}
		for _, c := range e.Capabilities {
			if !slices.Contains(parent.Capabilities, c) {
				add("capability %q is not in the parent", c)
			}
		}
		b, pb := e.Budget, parent.Budget
		if b.USD > pb.USD || b.Tokens > pb.Tokens || b.WallMS > pb.WallMS || b.MaxSubagents > pb.MaxSubagents {
			add("the child budget is above what is left in the parent")
		}
		if b.HopsRemaining > pb.HopsRemaining {
			add("the child has more hops than the parent")
		}
	}
	if len(p) > 0 {
		return &ValidationError{Problems: p}
	}
	return nil
}

// ErrNotFound means the envelope does not exist.
var ErrNotFound = errors.New("envelope not found")

// Service stores envelopes.
type Service struct{ e *drel.Engine }

// New returns the envelopes service.
func New(e *drel.Engine) *Service { return &Service{e: e} }

// Create validates and stores an envelope in the transaction in ctx. It assigns the ID,
// sets root_id for roots and fills the default acceptance check.
func (s *Service) Create(ctx context.Context, env *Envelope, parent *Remainder) error {
	if len(env.Acceptance) == 0 {
		env.Acceptance = []Check{DefaultCheck}
	}
	if env.ContextRefs == nil {
		env.ContextRefs = []ContextRef{}
	}
	if env.Capabilities == nil {
		env.Capabilities = []string{}
	}
	if env.Status == "" {
		env.Status = "created"
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), EnvelopeMeta)
		repo.Add(env) // stamps the ID
		if parent == nil {
			env.RootID = env.ID()
		}
		if env.IdempotencyKey == "" {
			env.IdempotencyKey = env.ID().String()
		}
		return Validate(env, parent)
	})
}

// Get returns an envelope of a space.
// Inside a transaction it reads on the transaction's connection.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Envelope, error) {
	var e *Envelope
	var err error
	if tx, ok := drel.FromContext(ctx); ok {
		e, err = drel.Repo(tx, EnvelopeMeta).AsNoTracking().Where(Envelopes.ID.Eq(id)).Where(Envelopes.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	} else {
		e, err = drel.NewRepository(s.e, EnvelopeMeta).Where(Envelopes.ID.Eq(id)).Where(Envelopes.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	}
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotFound
	}
	return e, nil
}

// SetStatus changes the status in the transaction in ctx.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status string, result *Result) error {
	if !slices.Contains(Statuses, status) {
		return fmt.Errorf("unknown envelope status %q", status)
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		env, err := drel.Repo(drel.MustFromContext(ctx), EnvelopeMeta).Where(Envelopes.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if env == nil {
			return ErrNotFound
		}
		env.Status = status
		if result != nil {
			env.Result = result
		}
		return nil
	})
}

// AddBudget raises the USD budget of an envelope (the "Budget reached" card).
func (s *Service) AddBudget(ctx context.Context, id uuid.UUID, usd float64) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		env, err := drel.Repo(drel.MustFromContext(ctx), EnvelopeMeta).Where(Envelopes.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil || env == nil {
			return ErrNotFound
		}
		b := env.Budget
		b.USD += usd
		env.Budget = b
		return nil
	})
}

// SetRoute records the router's step and decision (§9.3).
func (s *Service) SetRoute(ctx context.Context, id uuid.UUID, route Route) error {
	step := route.Step
	if !slices.Contains(Steps, step) {
		return fmt.Errorf("unknown step %q", step)
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		env, err := drel.Repo(drel.MustFromContext(ctx), EnvelopeMeta).Where(Envelopes.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if env == nil {
			return ErrNotFound
		}
		st := step
		env.Step = &st
		d := env.Decision
		r := route
		d.Route = &r
		env.Decision = d
		if step == "fanout" && !slices.Contains(env.Acceptance, CitationsCheck) {
			env.Acceptance = append(append([]Check{}, env.Acceptance...), CitationsCheck)
		}
		if env.Status == "created" {
			env.Status = "routed"
		}
		return nil
	})
}
