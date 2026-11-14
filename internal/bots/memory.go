package bots

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
)

// MemoryLimit is the size limit of a bot's MEMORY.md and of the shared memory (§6.3).
const MemoryLimit = 4096

// Errors of memory writes.
var (
	ErrMemoryFull    = errors.New("memory is full (4 KB): remove or shorten facts first")
	ErrFactNotFound  = errors.New("no fact matches this text")
	ErrFactAmbiguous = errors.New("more than one fact matches this text; give more of it")
)

func memKey(botID *uuid.UUID) string {
	if botID == nil {
		return ""
	}
	return botID.String()
}

// GetMemory returns the memory of a bot, or the shared memory when botID is nil.
func (s *Service) GetMemory(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID) (*Memory, error) {
	m, err := drel.NewRepository(s.e, MemoryMeta).Where(Memories.SpaceID.Eq(spaceID)).Where(Memories.BotKey.Eq(memKey(botID))).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return &Memory{SpaceID: spaceID, BotID: botID, BotKey: memKey(botID)}, nil
	}
	return m, nil
}

// SetMemory replaces the memory content. Content over 4 KB is refused.
func (s *Service) SetMemory(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, botID *uuid.UUID, content, origin string) (*Memory, error) {
	content = normalizeMemory(content)
	if len(content) > MemoryLimit {
		return nil, ErrMemoryFull
	}
	var out *Memory
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), MemoryMeta)
		m, err := repo.Where(Memories.SpaceID.Eq(spaceID)).Where(Memories.BotKey.Eq(memKey(botID))).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			m = &Memory{SpaceID: spaceID, BotID: botID, BotKey: memKey(botID)}
			repo.Add(m)
		}
		m.Content, m.Rev, m.UpdatedByKind, m.UpdatedBy, m.Origin = content, m.Rev+1, actor.Kind, actor.ID, origin
		out = m
		target := "shared_memory"
		if botID != nil {
			target = "bot_memory:" + botID.String()
		}
		if actor.Kind == "user" {
			return s.audit.Record(ctx, actor, &spaceID, "memory.edit", target, map[string]string{"bytes": fmt.Sprint(len(content))})
		}
		return nil
	})
	return out, err
}

func normalizeMemory(s string) string {
	lines := []string{}
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Remember adds a fact, or replaces the fact that matches replaces. It keeps one fact per line.
func (s *Service) Remember(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, botID *uuid.UUID, fact, replaces, origin string) (*Memory, error) {
	fact = oneLine(fact)
	if fact == "" {
		return nil, errors.New("the fact is empty")
	}
	m, err := s.GetMemory(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	lines := splitLines(m.Content)
	if replaces != "" {
		i, err := findFact(lines, replaces)
		if err != nil {
			return nil, err
		}
		lines[i] = "- " + fact
	} else {
		for _, l := range lines {
			if strings.EqualFold(strings.TrimPrefix(l, "- "), fact) {
				return m, nil
			}
		}
		lines = append(lines, "- "+fact)
	}
	return s.SetMemory(ctx, actor, spaceID, botID, strings.Join(lines, "\n"), origin)
}

// Forget removes the fact that matches text.
func (s *Service) Forget(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, botID *uuid.UUID, text, origin string) (*Memory, error) {
	m, err := s.GetMemory(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	lines := splitLines(m.Content)
	i, err := findFact(lines, text)
	if err != nil {
		return nil, err
	}
	lines = append(lines[:i], lines[i+1:]...)
	return s.SetMemory(ctx, actor, spaceID, botID, strings.Join(lines, "\n"), origin)
}

func splitLines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func findFact(lines []string, text string) (int, error) {
	text = strings.ToLower(oneLine(text))
	found := -1
	for i, l := range lines {
		ll := strings.ToLower(l)
		if strings.TrimPrefix(ll, "- ") == text {
			return i, nil
		}
		if strings.Contains(ll, text) {
			if found >= 0 {
				return -1, ErrFactAmbiguous
			}
			found = i
		}
	}
	if found < 0 {
		return -1, ErrFactNotFound
	}
	return found, nil
}

// OpenWorkList returns the open items of a bot as text lines with due times.
func (s *Service) OpenWorkList(ctx context.Context, botID uuid.UUID) ([]string, error) {
	items, err := drel.NewRepository(s.e, OpenWorkMeta).Where(OpenWorks.BotID.Eq(botID)).Where(OpenWorks.DoneAt.IsNull()).OrderBy(OpenWorks.CreatedAt.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		line := "[" + ShortID(it.ID()) + "] " + it.Text
		if it.DueAt != nil {
			line += " (due " + it.DueAt.UTC().Format("2006-01-02 15:04 UTC") + ")"
		}
		out = append(out, line)
	}
	return out, nil
}

// Errors of open work.
var (
	ErrWorkNotFound = errors.New("no open work item has this ID")
	ErrWorkEmpty    = errors.New("an open work item needs a text")
)

// AddOpenWork adds an item to a bot's open work list, with an optional due time.
func (s *Service) AddOpenWork(ctx context.Context, botID uuid.UUID, text string, due *time.Time) (*OpenWork, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrWorkEmpty
	}
	if due != nil {
		d := due.UTC()
		due = &d
	}
	it := &OpenWork{BotID: botID, Text: text, DueAt: due}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), OpenWorkMeta).Add(it)
		return nil
	})
	return it, err
}

// ShortID is the last 8 characters of an ID. The first characters of a UUIDv7 are its
// time, so items made in the same millisecond share them.
func ShortID(id uuid.UUID) string {
	s := id.String()
	return s[len(s)-8:]
}

// CompleteOpenWork marks an item done by its ID or its short ID.
func (s *Service) CompleteOpenWork(ctx context.Context, botID uuid.UUID, idPrefix string, now time.Time) (*OpenWork, error) {
	idPrefix = strings.ToLower(strings.Trim(strings.TrimSpace(idPrefix), "[]"))
	items, err := drel.NewRepository(s.e, OpenWorkMeta).Where(OpenWorks.BotID.Eq(botID)).Where(OpenWorks.DoneAt.IsNull()).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if idPrefix != "" && (it.ID().String() == idPrefix || ShortID(it.ID()) == idPrefix) {
			_, err := s.e.Exec(ctx, "UPDATE open_work SET done_at = $1, updated_at = $2 WHERE id = $3", now.UTC(), now.UTC(), it.ID())
			return it, err
		}
	}
	return nil, ErrWorkNotFound
}

// ClaimDue returns the open items that are due and not done, and marks each as woken in
// the same statement, so one item wakes its bot once even with several processes.
func (s *Service) ClaimDue(ctx context.Context, now time.Time) ([]*OpenWork, error) {
	due, err := drel.NewRepository(s.e, OpenWorkMeta).Where(OpenWorks.DoneAt.IsNull()).Where(OpenWorks.WokenAt.IsNull()).Where(OpenWorks.DueAt.LTE(&now)).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []*OpenWork
	for _, it := range due {
		n, err := s.e.Exec(ctx, "UPDATE open_work SET woken_at = $1 WHERE id = $2 AND woken_at IS NULL AND done_at IS NULL", now.UTC(), it.ID())
		if err != nil {
			return out, err
		}
		if n == 1 {
			out = append(out, it)
		}
	}
	return out, nil
}
