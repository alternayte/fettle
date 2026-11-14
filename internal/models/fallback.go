//go:build !drelgen

package models

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Resolved is a role target with its connection.
type Resolved struct {
	Target Target
	Conn   Conn
	Name   string // provider name, for events
}

// Switch records a move from one target to the next (the model_switch event).
type Switch struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// Retries before the next target, and the backoff between them.
var (
	RetriesPerTarget = 2
	RetryBackoff     = func(attempt int) time.Duration { return time.Duration(attempt) * 500 * time.Millisecond }
)

// CallWithFallback calls the targets in order (§8.3). On 429, 5xx or a timeout before the
// first token it retries a target 2 times with backoff, then uses the next target. A
// stream that fails after its first token is not retried. It returns the response and
// the index of the target that answered.
func CallWithFallback(ctx context.Context, hc *http.Client, targets []Resolved, req Request, onDelta func(string), onSwitch func(Switch)) (Response, int, error) {
	if len(targets) == 0 {
		return Response{}, -1, errors.New("the role has no model target")
	}
	var last error
	for i, t := range targets {
		ad, err := AdapterFor(t.Conn.APIType)
		if err != nil {
			return Response{}, i, err
		}
		r := req
		r.Model = t.Target.Model
		for attempt := 0; attempt <= RetriesPerTarget; attempt++ {
			if attempt > 0 {
				select {
				case <-time.After(RetryBackoff(attempt)):
				case <-ctx.Done():
					return Response{}, i, ctx.Err()
				}
			}
			resp, err := ad.Call(ctx, hc, t.Conn, r, onDelta)
			if err == nil {
				if resp.Model == "" {
					resp.Model = r.Model
				}
				return resp, i, nil
			}
			last = err
			if !IsRetryable(err) {
				return Response{}, i, err
			}
		}
		if i+1 < len(targets) && onSwitch != nil {
			onSwitch(Switch{From: t.Name + "/" + t.Target.Model, To: targets[i+1].Name + "/" + targets[i+1].Target.Model, Reason: last.Error()})
		}
	}
	return Response{}, len(targets) - 1, last
}
