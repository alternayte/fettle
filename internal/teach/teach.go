package teach

import (
	"context"
	"errors"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Errors of the teach service.
var (
	ErrNotFound = errors.New("teach session not found")
	ErrActive   = errors.New("this bot has a teach session that records now")
	ErrState    = errors.New("the teach session is not in a state that allows this")
)

// MaxNote is the longest note in characters.
const MaxNote = 2000

// Service keeps teach sessions and their steps.
type Service struct {
	e *drel.Engine
}

// New returns the service.
func New(e *drel.Engine) *Service { return &Service{e: e} }

// Create starts a session. A bot has at most one recording session.
func (s *Service) Create(ctx context.Context, sess *Session) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), SessionMeta)
		busy, err := repo.Where(Sessions.BotID.Eq(sess.BotID)).Where(Sessions.Status.Eq(StatusRecording)).Exists(ctx)
		if err != nil {
			return err
		}
		if busy {
			return ErrActive
		}
		sess.Status = StatusRecording
		if sess.Questions == nil {
			sess.Questions = []Question{}
		}
		repo.Add(sess)
		return nil
	})
}

// Get returns a session of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Session, error) {
	sess, err := drel.NewRepository(s.e, SessionMeta).Where(Sessions.ID.Eq(id)).Where(Sessions.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrNotFound
	}
	return sess, nil
}

// Find returns a session by ID only (jobs).
func (s *Service) Find(ctx context.Context, id uuid.UUID) (*Session, error) {
	sess, err := drel.NewRepository(s.e, SessionMeta).Where(Sessions.ID.Eq(id)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrNotFound
	}
	return sess, nil
}

// Recording returns the recording session of a bot, or nil.
func (s *Service) Recording(ctx context.Context, botID uuid.UUID) (*Session, error) {
	return drel.NewRepository(s.e, SessionMeta).Where(Sessions.BotID.Eq(botID)).Where(Sessions.Status.Eq(StatusRecording)).FirstOrNil(ctx)
}

// Modify loads a session in a transaction and saves what fn changes.
func (s *Service) Modify(ctx context.Context, id uuid.UUID, fn func(ctx context.Context, sess *Session) error) (*Session, error) {
	var out *Session
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		sess, err := drel.Repo(drel.MustFromContext(ctx), SessionMeta).Where(Sessions.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if sess == nil {
			return ErrNotFound
		}
		if err := fn(ctx, sess); err != nil {
			return err
		}
		out = sess
		return nil
	})
	return out, err
}

// Steps returns the steps of a session in order, with the removed ones when all is set.
func (s *Service) Steps(ctx context.Context, sessionID uuid.UUID, all bool) ([]*Step, error) {
	q := drel.NewRepository(s.e, StepMeta).Where(Steps.SessionID.Eq(sessionID))
	if !all {
		q = q.Where(Steps.Removed.Eq(false))
	}
	return q.OrderBy(Steps.Seq.Asc()).All(ctx)
}

// Step returns one step of a session.
func (s *Service) Step(ctx context.Context, sessionID uuid.UUID, seq int) (*Step, error) {
	st, err := drel.NewRepository(s.e, StepMeta).Where(Steps.SessionID.Eq(sessionID)).Where(Steps.Seq.Eq(seq)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, ErrNotFound
	}
	return st, nil
}

func maxSeq(ctx context.Context, tx *drel.Tx, sessionID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, "SELECT COALESCE(MAX(seq), 0) FROM teach_steps WHERE session_id = $1", sessionID).Scan(&n)
	return n, err
}

// AddSteps appends recorded steps and moves the pulled mark of the session from old to
// pulled. When another pull moved the mark first, it stores nothing.
func (s *Service) AddSteps(ctx context.Context, sessionID uuid.UUID, steps []*Step, old, pulled int) error {
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		moved, err := tx.Exec(ctx, "UPDATE teach_sessions SET pulled = $1 WHERE id = $2 AND pulled = $3", pulled, sessionID, old)
		if err != nil {
			return err
		}
		if moved != 1 {
			return errPulled
		}
		n, err := maxSeq(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		repo := drel.Repo(tx, StepMeta)
		for _, st := range steps {
			n++
			st.SessionID, st.Seq = sessionID, n
			if st.Notes == nil {
				st.Notes = []string{}
			}
			repo.Add(st)
		}
		return nil
	})
	if errors.Is(err, errPulled) {
		return nil
	}
	return err
}

var errPulled = errors.New("another pull stored these steps")

// AddNote attaches a note to the newest step. Without a step, the note is a step of its
// own: a part of the task in another app, or what comes first.
func (s *Service) AddNote(ctx context.Context, sessionID uuid.UUID, text string) (*Step, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("the note is empty")
	}
	if len([]rune(text)) > MaxNote {
		text = string([]rune(text)[:MaxNote])
	}
	var out *Step
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		repo := drel.Repo(tx, StepMeta)
		last, err := repo.Where(Steps.SessionID.Eq(sessionID)).Where(Steps.Removed.Eq(false)).OrderBy(Steps.Seq.Desc()).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if last != nil {
			last.Notes = append(last.Notes, text)
			out = last
			return nil
		}
		n, err := maxSeq(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		out = &Step{SessionID: sessionID, Seq: n + 1, Kind: "note", Notes: []string{text}}
		repo.Add(out)
		return nil
	})
	return out, err
}

// NewNoteStep adds a note as a step of its own after the newest step.
func (s *Service) NewNoteStep(ctx context.Context, sessionID uuid.UUID, text string) (*Step, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("the note is empty")
	}
	if len([]rune(text)) > MaxNote {
		text = string([]rune(text)[:MaxNote])
	}
	var out *Step
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		n, err := maxSeq(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		out = &Step{SessionID: sessionID, Seq: n + 1, Kind: "note", Notes: []string{text}}
		drel.Repo(tx, StepMeta).Add(out)
		return nil
	})
	return out, err
}

// StepEdit is a change of one step on the review card.
type StepEdit struct {
	Seq       int
	Value     *string
	Command   *string
	Notes     []string
	VaultItem *string
	Removed   *bool
}

// EditSteps applies review edits to the steps of a session.
func (s *Service) EditSteps(ctx context.Context, sessionID uuid.UUID, edits []StepEdit) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), StepMeta)
		for _, ed := range edits {
			st, err := repo.Where(Steps.SessionID.Eq(sessionID)).Where(Steps.Seq.Eq(ed.Seq)).FirstOrNil(ctx)
			if err != nil {
				return err
			}
			if st == nil {
				return ErrNotFound
			}
			if ed.Value != nil && st.Secret == "" {
				st.Value = *ed.Value
			}
			if ed.Command != nil && st.Kind == "command" {
				st.Command = *ed.Command
			}
			if ed.Notes != nil {
				st.Notes = ed.Notes
			}
			if ed.VaultItem != nil {
				st.VaultItem = strings.TrimSpace(*ed.VaultItem)
			}
			if ed.Removed != nil {
				st.Removed = *ed.Removed
			}
		}
		return nil
	})
}
