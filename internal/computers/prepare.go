package computers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PrepareStatus is the background preparation of one provider (§15.3, step 2).
type PrepareStatus struct {
	Provider string     `json:"provider"`
	State    string     `json:"state"` // idle, running, ready, failed
	Steps    []Progress `json:"steps"`
	Error    string     `json:"error,omitempty"`
	// Image is the computer image that this preparation got.
	Image string `json:"image,omitempty"`
}

// PreparingError means the provider still prepares (runtime or image download), so a run
// waits for it.
type PreparingError struct{ Detail string }

func (e *PreparingError) Error() string { return "the computer is being prepared: " + e.Detail }

type preparer struct {
	mu     sync.Mutex
	status map[string]*PrepareStatus
}

func (m *Manager) prep() *preparer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.preps == nil {
		m.preps = &preparer{status: map[string]*PrepareStatus{}}
	}
	return m.preps
}

// PrepareStatusOf returns the preparation of a provider.
func (m *Manager) PrepareStatusOf(kind string) PrepareStatus {
	p := m.prep()
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.status[kind]; ok {
		out := *s
		out.Steps = append([]Progress(nil), s.Steps...)
		return out
	}
	return PrepareStatus{Provider: kind, State: "idle", Steps: []Progress{}}
}

// StartPrepare prepares a provider in the background: its runtime and the computer image.
// A second call while it runs, or after it is ready, does nothing.
func (m *Manager) StartPrepare(kind string) error {
	prov, ok := m.Providers[kind]
	if !ok {
		return fmt.Errorf("the %s provider is not available on this computer", kind)
	}
	p := m.prep()
	p.mu.Lock()
	image := m.image()
	if s, ok := p.status[kind]; ok && (s.State == "running" || (s.State == "ready" && s.Image == image)) {
		p.mu.Unlock()
		return nil
	}
	st := &PrepareStatus{Provider: kind, State: "running", Steps: []Progress{}, Image: image}
	p.status[kind] = st
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		err := prov.Prepare(ctx, func(pr Progress) {
			p.mu.Lock()
			defer p.mu.Unlock()
			for i := range st.Steps {
				if st.Steps[i].Step == pr.Step {
					st.Steps[i] = pr
					return
				}
			}
			st.Steps = append(st.Steps, pr)
		})
		p.mu.Lock()
		defer p.mu.Unlock()
		if err != nil {
			st.State, st.Error = "failed", err.Error()
			if m.Log != nil {
				m.Log.Warn("prepare provider", "provider", kind, "error", err)
			}
			return
		}
		st.State = "ready"
	}()
	return nil
}

// ErrPrepare is a provider preparation that failed (image pull, runtime install).
var ErrPrepare = errors.New("the computer could not be prepared")

// ensurePrepared lets a machine start only on a prepared provider. It starts the
// preparation when it has not run and reports a PreparingError while it runs.
func (m *Manager) ensurePrepared(kind string) error {
	s := m.PrepareStatusOf(kind)
	if s.State == "ready" && s.Image != m.image() {
		// The image changed in the settings: prepare the new one.
		s.State = "idle"
	}
	switch s.State {
	case "ready":
		return nil
	case "failed":
		// The next attempt tries again (a network error can pass).
		if err := m.StartPrepare(kind); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s", ErrPrepare, s.Error)
	case "idle":
		if err := m.StartPrepare(kind); err != nil {
			return err
		}
	}
	// A cached image prepares in moments; only a long download makes the run wait.
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		switch m.PrepareStatusOf(kind).State {
		case "ready":
			if m.PrepareStatusOf(kind).Image == m.image() {
				return nil
			}
		case "failed":
			return fmt.Errorf("%w: %s", ErrPrepare, m.PrepareStatusOf(kind).Error)
		}
	}
	s = m.PrepareStatusOf(kind)
	detail := "starting"
	for _, st := range s.Steps {
		if st.State == "running" {
			detail = st.Step
			if st.TotalMB > 0 {
				detail = st.Detail
			}
			if st.Percent > 0 {
				detail = fmt.Sprintf("%s %d%%", detail, st.Percent)
			}
		}
	}
	return &PreparingError{Detail: detail}
}
