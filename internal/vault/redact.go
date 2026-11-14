//go:build !drelgen

package vault

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// minSecretLen keeps very short values out of the replacer; they would match ordinary text.
const minSecretLen = 4

// Redactor replaces known secret values with [secret:<name>] (§11.4). It knows every
// vault value of a space, plus extra values that other features register, such as
// computer tokens and OAuth tokens.
type Redactor struct {
	v *Vault

	mu     sync.RWMutex
	spaces map[uuid.UUID]*strings.Replacer
	extra  map[uuid.UUID]map[string]string // space -> value -> name
	global map[string]string               // value -> name, for every space
}

func newRedactor(v *Vault) *Redactor {
	return &Redactor{v: v, spaces: map[uuid.UUID]*strings.Replacer{}, extra: map[uuid.UUID]map[string]string{}, global: map[string]string{}}
}

func (r *Redactor) invalidate(spaceID uuid.UUID) {
	r.mu.Lock()
	delete(r.spaces, spaceID)
	r.mu.Unlock()
}

// Register adds a value to redact in one space, such as an OAuth token.
func (r *Redactor) Register(spaceID uuid.UUID, name, value string) {
	if len(value) < minSecretLen {
		return
	}
	r.mu.Lock()
	if r.extra[spaceID] == nil {
		r.extra[spaceID] = map[string]string{}
	}
	r.extra[spaceID][value] = name
	delete(r.spaces, spaceID)
	r.mu.Unlock()
}

// RegisterGlobal adds a value to redact in every space, such as a model key or a computer token.
func (r *Redactor) RegisterGlobal(name, value string) {
	if len(value) < minSecretLen {
		return
	}
	r.mu.Lock()
	r.global[value] = name
	r.spaces = map[uuid.UUID]*strings.Replacer{}
	r.mu.Unlock()
}

// Forget removes an extra value.
func (r *Redactor) Forget(spaceID uuid.UUID, value string) {
	r.mu.Lock()
	delete(r.extra[spaceID], value)
	delete(r.global, value)
	r.spaces = map[uuid.UUID]*strings.Replacer{}
	r.mu.Unlock()
}

func (r *Redactor) replacer(ctx context.Context, spaceID uuid.UUID) (*strings.Replacer, error) {
	r.mu.RLock()
	rep, ok := r.spaces[spaceID]
	r.mu.RUnlock()
	if ok {
		return rep, nil
	}
	pairs := map[string]string{}
	items, err := r.v.List(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		val, err := r.v.decrypt(ctx, it)
		if err != nil || len(val) < minSecretLen {
			continue
		}
		pairs[val] = "[secret:" + it.Name + "]"
	}
	r.mu.RLock()
	for val, name := range r.extra[spaceID] {
		pairs[val] = "[secret:" + name + "]"
	}
	for val, name := range r.global {
		pairs[val] = "[secret:" + name + "]"
	}
	r.mu.RUnlock()
	// Longest values first, so a value that contains another is replaced whole.
	vals := make([]string, 0, len(pairs))
	for v := range pairs {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	args := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		args = append(args, v, pairs[v])
	}
	rep = strings.NewReplacer(args...)
	r.mu.Lock()
	r.spaces[spaceID] = rep
	r.mu.Unlock()
	return rep, nil
}

// String redacts s for a space. If the vault cannot be read, it fails closed and
// returns a fixed text instead of s.
func (r *Redactor) String(ctx context.Context, spaceID uuid.UUID, s string) string {
	if s == "" {
		return s
	}
	rep, err := r.replacer(ctx, spaceID)
	if err != nil {
		return "[redaction failed]"
	}
	return rep.Replace(s)
}

// Global redacts values that are not bound to one space, such as log lines. It applies
// the global values and the values of every space that the redactor has loaded.
func (r *Redactor) Global(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	reps := make([]*strings.Replacer, 0, len(r.spaces))
	for _, rep := range r.spaces {
		reps = append(reps, rep)
	}
	global := make(map[string]string, len(r.global))
	for v, n := range r.global {
		global[v] = n
	}
	r.mu.RUnlock()
	for _, rep := range reps {
		s = rep.Replace(s)
	}
	for v, name := range global {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, "[secret:"+name+"]")
		}
	}
	return s
}
