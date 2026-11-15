package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/usage"
)

func TestSCN_OPS_005_UsageExportTotalsEqualTheRows(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		chief := e.Chief()
		run, _ := e.Post(chief.ChatID, "Say hello.")
		e.WaitRun(run, "done")
		// Computer activity: 2,500 seconds in rows that share one time, past a page of 1000.
		ctx := context.Background()
		sp, bot := uuid.MustParse(e.Space), uuid.MustParse(chief.ID)
		at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
		for i := 0; i < 2500; i++ {
			if err := e.App.Usage.Record(ctx, &usage.UsageEvent{SpaceID: sp, Kind: "computer", BotID: &bot, Quantity: 1, Unit: "seconds", Size: "browser", CostUSD: 0.0001, Reported: true, At: at}); err != nil {
				t.Fatal(err)
			}
		}

		var rows int
		var tokens, seconds int64
		var cost float64
		if err := e.App.DB.Engine.QueryRow(ctx, "SELECT COUNT(*), COALESCE(SUM(CASE WHEN unit = 'tokens' THEN quantity ELSE 0 END), 0), COALESCE(SUM(CASE WHEN unit = 'seconds' THEN quantity ELSE 0 END), 0), COALESCE(SUM(cost_usd), 0) FROM usage_events").Scan(&rows, &tokens, &seconds, &cost); err != nil {
			t.Fatal(err)
		}

		req, _ := http.NewRequest("GET", e.URL+"/api/v1/admin/usage", nil)
		req.Header.Set("Authorization", "Bearer "+e.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-ndjson" {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("export: %d %s", resp.StatusCode, b)
		}
		var n int
		var gotTokens, gotSeconds int64
		var gotCost float64
		ids := map[string]bool{}
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var l struct {
				ID       string  `json:"id"`
				SpaceID  string  `json:"space_id"`
				Kind     string  `json:"kind"`
				Quantity int64   `json:"quantity"`
				Unit     string  `json:"unit"`
				CostUSD  float64 `json:"cost_usd"`
			}
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				t.Fatalf("line %d: %v", n+1, err)
			}
			if ids[l.ID] {
				t.Fatalf("row %s twice", l.ID)
			}
			ids[l.ID] = true
			n++
			if l.Unit == "tokens" {
				gotTokens += l.Quantity
			} else {
				gotSeconds += l.Quantity
			}
			gotCost += l.CostUSD
		}
		if n != rows || gotTokens != tokens || gotSeconds != seconds || math.Abs(gotCost-cost) > 1e-9 {
			t.Fatalf("export %d rows, %d tokens, %d s, $%f; table %d rows, %d tokens, %d s, $%f", n, gotTokens, gotSeconds, gotCost, rows, tokens, seconds, cost)
		}
		if tokens == 0 || seconds != 2500 {
			t.Fatalf("the scenario lacks model or computer usage: %d tokens, %d s", tokens, seconds)
		}
	})
}
