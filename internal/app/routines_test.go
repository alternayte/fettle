package app_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	fakepush "github.com/alternayte/fettle/internal/fakes/push"
	"github.com/alternayte/fettle/internal/testenv"
)

type routineOut struct {
	Routine struct {
		ID     string     `json:"id"`
		NextAt *time.Time `json:"next_at"`
	} `json:"routine"`
	Secret string `json:"secret"`
}

type routineRuns struct {
	Runs []struct {
		DueAt      time.Time `json:"due_at"`
		EnvelopeID string    `json:"envelope_id"`
		RunID      string    `json:"run_id"`
		Coalesced  bool      `json:"coalesced"`
		Missed     bool      `json:"missed"`
	} `json:"runs"`
}

func TestSCN_SKL_006_DowntimeCoalescesTicks(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		e.Fake.AddScript(fakemodel.Script{Name: "hourly", Match: fakemodel.Match{System: "Hourly check of the inbox"}, Steps: []fakemodel.Step{{Text: "Checked."}}})
		start := time.Date(2026, 3, 2, 8, 30, 0, 0, time.UTC)
		clock := start
		e.App.Routines.SetClock(func() time.Time { return clock })
		chief := e.Chief()
		var out routineOut
		e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "Hourly", "prompt": "Hourly check of the inbox.", "trigger": "cron", "cron": "0 * * * *", "tz": "UTC", "enabled": true}, &out)
		// Fettle is down from 08:30 to 13:30: five ticks, 09:00 to 13:00, come due.
		clock = start.Add(5 * time.Hour)
		e.App.Agent.SetClock(func() time.Time { return clock })
		ctx := context.Background()
		if n, err := e.App.Routines.Tick(ctx); err != nil || n != 1 {
			t.Fatalf("tick fired %d: %v", n, err)
		}
		if n, _ := e.App.Routines.Tick(ctx); n != 0 {
			t.Fatalf("a second tick fired %d more", n)
		}
		var runs routineRuns
		e.MustDo("GET", e.SP("/routines/%s/runs", out.Routine.ID), nil, &runs)
		fired, coalesced := 0, 0
		var runID string
		for _, r := range runs.Runs {
			if r.Coalesced {
				coalesced++
				continue
			}
			fired++
			runID = r.RunID
			if !r.DueAt.Equal(start.Add(30 * time.Minute)) {
				t.Fatalf("the firing is due at %s, want the first due time 09:00", r.DueAt)
			}
		}
		if fired != 1 || coalesced != 4 {
			t.Fatalf("%d firings and %d coalesced ticks, want 1 and 4", fired, coalesced)
		}
		e.WaitRun(runID, "done")
		found := false
		for _, q := range e.Fake.Requests() {
			if strings.Contains(q.System, "Hourly check") || strings.Contains(q.LastUser, "Hourly check") {
				found = found || (strings.Contains(q.System, "was due at 09:00") && strings.Contains(q.System, "started at 13:30"))
			}
		}
		if !found {
			t.Fatal("the late firing does not state the first due time and the start time")
		}
	})
}

func TestSCN_SKL_005_CronTimeZoneAndSignedWebhook(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		clock := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
		e.App.Routines.SetClock(func() time.Time { return clock })
		chief := e.Chief()
		var cronR routineOut
		e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "Morning", "prompt": "Write the morning note.", "trigger": "cron", "cron": "0 9 * * *", "tz": "America/New_York", "enabled": true}, &cronR)
		// 09:00 in New York in November is 14:00 UTC.
		if want := time.Date(2026, 11, 14, 14, 0, 0, 0, time.UTC); cronR.Routine.NextAt == nil || !cronR.Routine.NextAt.Equal(want) {
			t.Fatalf("next run %v, want %s", cronR.Routine.NextAt, want)
		}
		clock = time.Date(2026, 11, 14, 13, 59, 0, 0, time.UTC)
		ctx := context.Background()
		if n, _ := e.App.Routines.Tick(ctx); n != 0 {
			t.Fatal("the routine fired before its time")
		}
		clock = time.Date(2026, 11, 14, 14, 0, 20, 0, time.UTC)
		if n, err := e.App.Routines.Tick(ctx); err != nil || n != 1 {
			t.Fatalf("the routine did not fire at 09:00 New York: %d %v", n, err)
		}

		var hook routineOut
		e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "Deploys", "prompt": "Summarize the deploy event.", "trigger": "webhook", "enabled": true}, &hook)
		post := func(body []byte, sig string) int {
			req, _ := http.NewRequest("POST", e.URL+"/hooks/"+hook.Routine.ID, bytes.NewReader(body))
			req.Header.Set("X-Fettle-Signature", sig)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		body := []byte(`{"service":"api","status":"deployed"}`)
		mac := hmac.New(sha256.New, []byte(hook.Secret))
		mac.Write(body)
		if code := post(body, "sha256="+hex.EncodeToString(mac.Sum(nil))); code != 202 {
			t.Fatalf("a valid signature got %d", code)
		}
		if code := post([]byte("{not json"), "sha256="+strings.Repeat("ab", 32)); code != 401 {
			t.Fatalf("a bad signature on a body that does not parse got %d, want 401", code)
		}
		var origins []string
		rows, err := e.App.DB.Engine.Query(ctx, "SELECT origin, kind FROM envelopes WHERE space_id = $1 AND kind IN ('routine','webhook') ORDER BY created_at", uuid.MustParse(e.Space))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var o, k string
			_ = rows.Scan(&o, &k)
			origins = append(origins, k+":"+o)
		}
		rows.Close()
		if strings.Join(origins, ",") != "routine:routine,webhook:routine" {
			t.Fatalf("envelopes %v, want a routine and a webhook firing with origin routine", origins)
		}
		// Both runs have reduced intent: the goal is data, not an instruction.
		deadline := time.Now().Add(20 * time.Second)
		reduced := 0
		for reduced < 2 && time.Now().Before(deadline) {
			reduced = 0
			for _, q := range e.Fake.Requests() {
				for _, m := range q.Messages {
					if strings.Contains(m.Text, "it has reduced intent") {
						reduced++
						break
					}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if reduced < 2 {
			t.Fatalf("%d model requests show reduced intent, want 2", reduced)
		}
	})
}

func TestSCN_SKL_007_NotificationsLimitAndMissedTicks(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	pid := e.SetupModel("openai-chat", "", 128000)
	mock := fakepush.New()
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	mock.URL = srv.URL
	sub, err := mock.Subscribe("phone")
	if err != nil {
		t.Fatal(err)
	}
	e.MustDo("POST", "/push/subscriptions", map[string]any{"endpoint": sub.Endpoint, "keys": map[string]string{"p256dh": sub.P256dh, "auth": sub.Auth}}, nil)
	e.Fake.AddScript(fakemodel.Script{Name: "nightly", Match: fakemodel.Match{System: "Tidy the notes"}, Steps: []fakemodel.Step{{Text: "Tidied."}}})
	chief := e.Chief()
	var r routineOut
	e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "Nightly", "prompt": "Tidy the notes.", "trigger": "webhook", "notify": "on_failure", "enabled": true}, &r)
	var fired struct {
		EnvelopeID string `json:"envelope_id"`
	}
	runOf := func() string {
		var runs routineRuns
		e.MustDo("GET", e.SP("/routines/%s/runs", r.Routine.ID), nil, &runs)
		for _, rr := range runs.Runs {
			if rr.EnvelopeID == fired.EnvelopeID {
				return rr.RunID
			}
		}
		t.Fatal("no run of the firing")
		return ""
	}
	e.MustDo("POST", e.SP("/routines/%s/run", r.Routine.ID), nil, &fired)
	e.WaitRun(runOf(), "done")
	time.Sleep(500 * time.Millisecond)
	if n := len(mock.Deliveries()); n != 0 {
		t.Fatalf("a successful run sent %d pushes with on-failure notifications", n)
	}
	// The model now fails, so the next firing fails.
	e.MustDo("PATCH", e.SP("/model-providers/%s", pid), map[string]any{"name": "fake failing", "api_type": "openai-chat", "auth_style": "bearer", "base_url": e.FakeURL + "/mode/fail500=99/v1"}, nil)
	e.MustDo("POST", e.SP("/routines/%s/run", r.Routine.ID), nil, &fired)
	e.WaitRun(runOf(), "failed")
	deadline := time.Now().Add(10 * time.Second)
	for len(mock.Deliveries()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	ds := mock.Deliveries()
	if len(ds) != 1 || ds[0].Err != nil {
		t.Fatalf("a failed run sent %d pushes: %+v", len(ds), ds)
	}
	var n struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	_ = json.Unmarshal(ds[0].Payload, &n)
	if n.Type != "failed" || !strings.Contains(n.URL, chief.ChatID) {
		t.Fatalf("push payload %s", ds[0].Payload)
	}

	// A bot has at most 50 routines; Chief has its template routine and the one above.
	var have struct {
		Routines []struct {
			BotID string `json:"bot_id"`
		} `json:"routines"`
	}
	e.MustDo("GET", e.SP("/routines"), nil, &have)
	own := 0
	for _, r := range have.Routines {
		if r.BotID == chief.ID {
			own++
		}
	}
	for i := own; i < 50; i++ {
		e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": fmt.Sprintf("R%d", i), "prompt": "Check.", "trigger": "webhook", "enabled": false}, nil)
	}
	if code, _ := e.Do("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "R51", "prompt": "Check.", "trigger": "webhook", "enabled": false}, nil); code != 409 {
		t.Fatalf("the 51st routine got %d, want 409", code)
	}

	// A tick that came due while the app was closed is listed as missed.
	closed := time.Date(2026, 5, 4, 7, 50, 0, 0, time.UTC)
	clock := closed
	e.App.Routines.SetClock(func() time.Time { return clock })
	var daily routineOut
	e.MustDo("PUT", e.SP("/settings/routines.max_per_bot"), map[string]any{"value": 60}, nil)
	e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": chief.ID, "name": "Daily", "prompt": "Tidy the notes.", "trigger": "cron", "cron": "0 8 * * *", "tz": "UTC", "enabled": true}, &daily)
	e.App.Routines.Started = closed.Add(3 * time.Hour) // the app opens again at 10:50
	clock = closed.Add(3*time.Hour + time.Minute)
	if _, err := e.App.Routines.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var runs routineRuns
	e.MustDo("GET", e.SP("/routines/%s/runs", daily.Routine.ID), nil, &runs)
	if len(runs.Runs) != 1 || !runs.Runs[0].Missed {
		t.Fatalf("the 08:00 tick is not listed as missed: %+v", runs.Runs)
	}
}
