//go:build e2e

package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/computers"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/fakes/web"
)

// teamApp starts an in-process app with Docker computers and the fixture sites.
func teamApp(t *testing.T) *apptest.Env {
	t.Helper()
	hosts, _ := fixtureHosts(t)
	t.Setenv("FETTLE_COMPUTER_HOSTS", hosts)
	return dockerApp(t)
}

// researchFanout scripts the router, three helpers that read docs.test, and the owner's
// synthesis for a marker.
func researchFanout(e *apptest.Env, marker string, synthesis ...fakemodel.Step) {
	// The fake prefers the newest script, so the owner script goes first: helpers share
	// the owner's instructions and must match their part scripts.
	e.Fake.AddScript(fakemodel.Script{Name: "owner-" + marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: synthesis})
	var parts []map[string]any
	for _, p := range web.Products {
		parts = append(parts, map[string]any{"goal": fmt.Sprintf("[[part-%s-%s]] Find the price and weight of the %s on http://docs.test/%s.", marker, p.Slug, p.Name, p.Slug), "acceptance": "The price and weight have the page as source.", "needs": "read"})
		url := "http://docs.test/" + p.Slug
		e.Fake.AddScript(fakemodel.Script{Name: "part-" + marker + p.Slug, Match: fakemodel.Match{System: "You are a helper", User: fmt.Sprintf("[[part-%s-%s]]", marker, p.Slug)}, Steps: []fakemodel.Step{
			{ToolCalls: call("browser_navigate", map[string]any{"url": url})},
			{ToolCalls: call("report_result", map[string]any{"summary": fmt.Sprintf("%s: $%d, %.1f kg.", p.Name, p.PriceUSD, p.WeightKG), "findings": []map[string]any{{"claim": fmt.Sprintf("The %s costs $%d and weighs %.1f kg.", p.Name, p.PriceUSD, p.WeightKG), "sources": []string{url}}}, "open_items": []string{}})},
		}})
	}
	e.Fake.AddScript(fakemodel.Script{Name: "route-" + marker, Match: fakemodel.Match{System: "You route one piece of work", User: "[[" + marker + "]]"}, Steps: []fakemodel.Step{
		{JSON: map[string]any{"step": "fanout", "owner_bot_id": "", "parts": parts, "rationale": "three independent pages", "confidence": 0.9}},
	}})
}

func table(price map[string]int) string {
	var b strings.Builder
	b.WriteString("| Desk | Price | Weight |\n|---|---|---|\n")
	for _, p := range web.Products {
		fmt.Fprintf(&b, "| %s | $%d | %.1f kg [%s](http://docs.test/%s) |\n", p.Name, price[p.Slug], p.WeightKG, p.Slug, p.Slug)
	}
	return b.String()
}

func newBot(e *apptest.Env, name, job, marker string) apptest.Bot {
	e.T.Helper()
	var b apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": name, "job": job, "zone_id": zoneID(e, "browsing")}, &b)
	e.MustDo("PATCH", e.SP("/bots/%s", b.ID), map[string]any{"name": name, "job": job, "instructions": "[[script:" + marker + "]]"}, nil)
	return b
}

type routeEvent struct {
	Step  string   `json:"step"`
	Rules []string `json:"rules"`
	Parts []struct {
		Goal string `json:"goal"`
	} `json:"parts"`
}

func routeOf(t *testing.T, r apptest.Run) routeEvent {
	t.Helper()
	for _, ev := range r.Events {
		if ev.Type == "route_decision" {
			var d routeEvent
			_ = json.Unmarshal(ev.Data, &d)
			return d
		}
	}
	t.Fatalf("no route_decision: %s", r.EventTypes())
	return routeEvent{}
}

func verifyResults(t *testing.T, r apptest.Run) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range r.Events {
		if ev.Type == "verify_result" {
			var d map[string]any
			_ = json.Unmarshal(ev.Data, &d)
			out = append(out, d)
		}
	}
	return out
}

func TestSCN_E2E_001_FanoutCompareVerifiedTable(t *testing.T) {
	e := teamApp(t)
	right := map[string]int{}
	for _, p := range web.Products {
		right[p.Slug] = p.PriceUSD
	}
	researchFanout(e, "compare", fakemodel.Step{Text: "Here is the comparison:\n\n" + table(right)})
	research := newBot(e, "Research", "Finds and compares sources", "compare")
	run, _ := e.Post(research.ChatID, "[[compare]] Compare the three desks on docs.test and write a table.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	if r.Line.State != "done" {
		t.Fatalf("run %s: %s", r.Line.State, r.EventTypes())
	}
	if d := routeOf(t, r); d.Step != "fanout" || len(d.Parts) != 3 {
		t.Fatalf("route %+v", d)
	}
	vr := verifyResults(t, r)
	if len(vr) != 1 || vr[0]["pass"] != true || !strings.Contains(fmt.Sprint(vr[0]["checks"]), "citations") {
		t.Fatalf("verify results %v", vr)
	}
	var line struct {
		Line struct {
			Helpers  int   `json:"helpers"`
			Verified *bool `json:"verified"`
			Step     string
		} `json:"line"`
	}
	e.MustDo("GET", e.SP("/runs/%s", run), nil, &line)
	if line.Line.Helpers != 3 || line.Line.Verified == nil || !*line.Line.Verified || line.Line.Step != "fanout" {
		t.Fatalf("run line %+v", line.Line)
	}
	found := false
	for _, m := range e.Messages(research.ChatID) {
		found = found || strings.Contains(fmt.Sprint(m["body"]), "| Beta Desk | $610 |")
	}
	if !found {
		t.Fatal("the chat has no table")
	}
	// Save as skill keeps the helpers' work as steps (§13.1).
	var draft struct {
		SkillMD string `json:"skill_md"`
	}
	e.MustDo("POST", e.SP("/runs/%s/draft-skill", run), nil, &draft)
	if !strings.Contains(draft.SkillMD, "Split the work into 3 helpers") || !strings.Contains(draft.SkillMD, "     - Use `browser_") {
		t.Fatalf("the skill draft has no helper steps:\n%s", draft.SkillMD)
	}
}

func TestSCN_TEAM_006_FailedCitationsRetryThenEscalate(t *testing.T) {
	e := teamApp(t)
	wrong := map[string]int{}
	for _, p := range web.Products {
		wrong[p.Slug] = p.PriceUSD + 55
	}
	// The owner reasons next to a tool call, then writes a table with wrong prices. A retry
	// (after the verifier's reasons) repeats the same two steps.
	researchFanout(e, "wrong",
		fakemodel.Step{Text: "[[owner-reasoning]] I will guess the prices.", ToolCalls: call("search_history", map[string]any{"query": "desk prices"})},
		fakemodel.Step{Text: "Here is the comparison:\n\n" + table(wrong)})
	research := newBot(e, "Research", "Finds and compares sources", "wrong")
	run, _ := e.Post(research.ChatID, "[[wrong]] Compare the three desks on docs.test and write a table.")
	r := e.WaitRun(run, "escalated", "done", "failed")
	vr := verifyResults(t, r)
	if r.Line.State != "escalated" || len(vr) != 2 || vr[0]["pass"] != false || vr[1]["pass"] != false {
		t.Fatalf("run %s with verify results %v: %s", r.Line.State, vr, r.EventTypes())
	}
	if !strings.Contains(fmt.Sprint(vr[0]["checks"]), "0 of 3 claims") {
		t.Fatalf("citations reason %v", vr[0]["checks"])
	}
	var card map[string]any
	for _, m := range e.Messages(research.ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == "verification" {
			card = c
		}
	}
	if card == nil || card["status"] != "pending" || !strings.Contains(fmt.Sprint(card["detail"]), "citations") {
		t.Fatalf("no escalation card with the failed checks: %v", card)
	}
	for _, q := range e.Fake.Requests() {
		if strings.HasPrefix(q.System, "You verify a result of a bot") && strings.Contains(q.LastUser, "[[owner-reasoning]]") {
			t.Fatal("the verifier saw the owner's reasoning")
		}
	}
	// "Accept anyway" ends the work as done, not verified.
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "accept"}, nil)
	if r := e.WaitRun(run, "done"); len(verifyResults(t, r)) != 2 {
		t.Fatalf("accept started another verification: %s", r.EventTypes())
	}
}

func TestSCN_TEAM_003_FanoutHelpersReadOnlyWithBriefs(t *testing.T) {
	e := teamApp(t)
	right := map[string]int{}
	for _, p := range web.Products {
		right[p.Slug] = p.PriceUSD
	}
	researchFanout(e, "facts", fakemodel.Step{Text: table(right)})
	research := newBot(e, "Research", "Finds and compares sources", "facts")
	run, _ := e.Post(research.ChatID, "[[facts]] Compare the facts on the three docs.test pages.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	var started struct {
		Brief string `json:"brief"`
		Runs  []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
	}
	results := 0
	for _, ev := range r.Events {
		switch ev.Type {
		case "subagents_started":
			_ = json.Unmarshal(ev.Data, &started)
		case "subagent_result":
			var d struct {
				Status string `json:"status"`
				Result struct {
					Findings []struct {
						Claim   string   `json:"claim"`
						Sources []string `json:"sources"`
					} `json:"findings"`
				} `json:"result"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			if d.Status != "done" || len(d.Result.Findings) == 0 || !strings.HasPrefix(d.Result.Findings[0].Sources[0], "http://docs.test/") {
				t.Fatalf("helper result %s", ev.Data)
			}
			results++
		}
	}
	if len(started.Runs) != 3 || results != 3 {
		t.Fatalf("%d helpers started, %d results: %s", len(started.Runs), results, r.EventTypes())
	}
	if started.Brief == "" || len(started.Brief) > 2000*4 {
		t.Fatalf("brief of %d characters", len(started.Brief))
	}
	// Each helper sees the brief, not the owner's chat, and has read tools only.
	write := []string{"exec", "write_file", "edit_file", "fill_login", "use_secret", "remember", "request_takeover", "handoff", "propose_bot", "sleep_until"}
	helpers := 0
	for _, q := range e.Fake.Requests() {
		if !strings.Contains(q.System, "You are a helper") || !strings.Contains(q.LastUser, "[[part-facts-") {
			continue
		}
		helpers++
		for _, tool := range q.Tools {
			for _, w := range write {
				if tool == w {
					t.Fatalf("a helper has the tool %s", tool)
				}
			}
		}
		if !strings.Contains(fmt.Sprint(q.Messages), "Context brief from the owner") || strings.Contains(fmt.Sprint(q.Messages), "Older messages are not shown") {
			t.Fatal("a helper got the owner's chat instead of the brief")
		}
		if len(q.Messages) == 2 && strings.Count(fmt.Sprint(q.Messages), "[[facts]] Compare") > 1 {
			t.Fatal("a helper got the owner's chat message as well as the brief")
		}
	}
	if helpers < 3 {
		t.Fatalf("%d helper requests", helpers)
	}
	// Each helper had its own browser context: three distinct sub-agent runs browsed.
	ids := map[string]bool{}
	for _, s := range started.Runs {
		sub := e.GetRun(s.RunID)
		if !sub.HasEvent("tool_call") {
			t.Fatalf("helper %s did not browse", s.RunID)
		}
		ids[s.RunID] = true
	}
	if len(ids) != 3 {
		t.Fatal("helpers share a run")
	}
}

func TestSCN_TEAM_005_HelperLimitsAndFailures(t *testing.T) {
	e := teamApp(t)
	e.MustDo("PUT", e.SP("/settings/agent.max_turns"), map[string]any{"value": 4}, nil)
	e.Fake.AddScript(fakemodel.Script{Name: "owner-limits", Match: fakemodel.Match{System: "[[script:limits]]"}, Steps: []fakemodel.Step{
		{Text: "The Alpha Desk costs $420 [alpha](http://docs.test/alpha). The beta helper failed."},
	}})
	parts := []map[string]any{
		{"goal": "[[part-limits-a]] Read the alpha page.", "acceptance": "Facts with sources.", "needs": "read"},
		{"goal": "[[part-limits-b]] Read the beta page forever.", "acceptance": "Facts with sources.", "needs": "read"},
	}
	e.Fake.AddScript(fakemodel.Script{Name: "route-limits", Match: fakemodel.Match{System: "You route one piece of work", User: "[[limits]]"}, Steps: []fakemodel.Step{
		{JSON: map[string]any{"step": "fanout", "owner_bot_id": "", "parts": parts, "rationale": "two pages", "confidence": 0.9}},
	}})
	// Helper a tries the tools that helpers never get, then reports.
	e.Fake.AddScript(fakemodel.Script{Name: "part-a", Match: fakemodel.Match{System: "You are a helper", User: "[[part-limits-a]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{
			{Name: "handoff", Args: map[string]any{"bot": "Writer", "goal": "Write it."}},
			{Name: "request_takeover", Args: map[string]any{"reason": "Help."}},
			{Name: "start_helpers", Args: map[string]any{"parts": []string{"x"}}},
		}},
		{ToolCalls: call("report_result", map[string]any{"summary": "Alpha read.", "findings": []map[string]any{{"claim": "The Alpha Desk costs $420.", "sources": []string{"http://docs.test/alpha"}}}})},
	}})
	// Helper b never stops, so it reaches the turn limit and fails.
	e.Fake.AddScript(fakemodel.Script{Name: "part-b", Match: fakemodel.Match{System: "You are a helper", User: "[[part-limits-b]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("browser_navigate", map[string]any{"url": "http://docs.test/beta"}), Repeat: true},
	}})
	research := newBot(e, "Research", "Finds and compares sources", "limits")
	run, _ := e.Post(research.ChatID, "[[limits]] Read the alpha and beta pages.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	if r.Line.State == "failed" {
		t.Fatalf("a failed helper failed the owner run: %s", r.EventTypes())
	}
	var statuses []string
	for _, ev := range r.Events {
		if ev.Type == "subagent_result" {
			var d struct {
				Status string `json:"status"`
				RunID  string `json:"run_id"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			statuses = append(statuses, d.Status)
			if d.Status == "done" {
				sub := e.GetRun(d.RunID)
				res := toolResults(sub, "handoff")
				res = append(res, toolResults(sub, "request_takeover")...)
				res = append(res, toolResults(sub, "start_helpers")...)
				if len(res) != 3 {
					t.Fatalf("helper a calls %v", res)
				}
				for _, x := range res {
					if !strings.Contains(x, "not available") {
						t.Fatalf("a helper call was not refused: %s", x)
					}
				}
			}
		}
	}
	if !strings.Contains(strings.Join(statuses, ","), "done") || !strings.Contains(strings.Join(statuses, ","), "failed") {
		t.Fatalf("helper statuses %v", statuses)
	}
	if !r.HasEvent("model_turn") {
		t.Fatalf("the owner did not continue after the failure: %s", r.EventTypes())
	}
}

func TestSCN_TEAM_008_HandoffWaitFilesAndLoopGuard(t *testing.T) {
	e := dockerApp(t)
	var inbox, writer apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Inbox", "job": "Triage mail", "zone_id": zoneID(e, "personal")}, &inbox)
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Writer", "job": "Writes summaries", "zone_id": zoneID(e, "browsing")}, &writer)
	setScript := func(b apptest.Bot, marker string, steps ...fakemodel.Step) {
		e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: steps})
		e.MustDo("PATCH", e.SP("/bots/%s", b.ID), map[string]any{"name": b.Name, "job": "x", "instructions": "[[script:" + marker + "]]"}, nil)
	}
	setScript(writer, "writer",
		fakemodel.Step{ToolCalls: call("exec", map[string]any{"command": "cat /home/fettle/shared/inbox/*/notes.txt"})},
		fakemodel.Step{Text: "Summary: {{tool_result}}"})
	setScript(inbox, "inbox",
		fakemodel.Step{ToolCalls: call("write_file", map[string]any{"path": "shared/notes.txt", "content": "The Q3 total is 40 units."})},
		fakemodel.Step{ToolCalls: call("handoff", map[string]any{"bot": "Writer", "goal": "Summarize the Q3 notes.", "context_refs": []map[string]any{{"type": "file", "ref": "shared/notes.txt"}}, "wait": true})},
		fakemodel.Step{Text: "Writer says: {{tool_result}}"})
	run, _ := e.Post(inbox.ChatID, "Get the Q3 notes summarized.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	res := toolResults(r, "handoff")
	if r.Line.State != "done" || len(res) != 1 || !strings.Contains(res[0], "The Q3 total is 40 units.") || !strings.Contains(res[0], "Writer finished") {
		t.Fatalf("run %s, handoff results %v: %s", r.Line.State, res, r.EventTypes())
	}
	if !r.HasEvent("handoff_sent") || !r.HasEvent("handoff_result") {
		t.Fatalf("events %s", r.EventTypes())
	}

	// The loop guard refuses the fourth handoff of one goal in one tree.
	again := fakemodel.Step{ToolCalls: call("handoff", map[string]any{"bot": "Writer", "goal": "Draft the weekly note!", "wait": false})}
	setScript(writer, "writer2", fakemodel.Step{Text: "Drafted."})
	setScript(inbox, "inbox2", again, again, again, again, fakemodel.Step{Text: "Done handing off."})
	run, _ = e.Post(inbox.ChatID, "Hand off the weekly note.")
	r = e.WaitRun(run, "done", "failed", "escalated")
	res = toolResults(r, "handoff")
	// The results of the handoffs that went out can steer the run again, so the script can
	// try more handoffs; each try after the third is refused.
	if len(res) < 4 {
		t.Fatalf("handoff results %v: %s", res, r.EventTypes())
	}
	for i, x := range res {
		if (i < 3) == strings.Contains(x, "loop guard") {
			t.Fatalf("handoff %d: %q; results %v", i+1, x, res)
		}
	}

	// With no hops left, a handoff is refused.
	e.MustDo("PUT", e.SP("/settings/budget.hops"), map[string]any{"value": 0}, nil)
	setScript(inbox, "inbox3", fakemodel.Step{ToolCalls: call("handoff", map[string]any{"bot": "Writer", "goal": "Write anything.", "wait": true})}, fakemodel.Step{Text: "Could not."})
	run, _ = e.Post(inbox.ChatID, "Hand off once more.")
	r = e.WaitRun(run, "done", "failed", "escalated")
	if res := toolResults(r, "handoff"); len(res) != 1 || !strings.Contains(res[0], "No hops are left") {
		t.Fatalf("hops 0: %v", res)
	}
}

func TestSCN_TEAM_004_ForkedDiffsMergeAndConflict(t *testing.T) {
	e := dockerApp(t)
	var writer apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Writer", "job": "Edits notes", "zone_id": zoneID(e, "personal")}, &writer)
	e.MustDo("PATCH", e.SP("/bots/%s", writer.ID), map[string]any{"name": "Writer", "job": "Edits notes", "instructions": "[[script:writer]]"}, nil)
	add := func(name string, m fakemodel.Match, steps ...fakemodel.Step) {
		e.Fake.AddScript(fakemodel.Script{Name: name, Match: m, Steps: steps})
	}
	write := func(path, content string) fakemodel.Step {
		return fakemodel.Step{ToolCalls: call("write_file", map[string]any{"path": path, "content": content})}
	}
	add("seed", fakemodel.Match{System: "[[script:writer]]", User: "[[seed]]"}, write("a.txt", "base A"), write("b.txt", "base B"), fakemodel.Step{Text: "Seeded."})
	run, _ := e.Post(writer.ChatID, "[[seed]] Make the two notes.")
	if r := e.WaitRun(run, "done", "failed", "escalated"); r.Line.State != "done" {
		t.Fatalf("seed: %s", r.EventTypes())
	}

	parts := []map[string]any{
		{"goal": "[[fork-1]] Rewrite a.txt.", "acceptance": "a.txt is new.", "needs": "write_local"},
		{"goal": "[[fork-2]] Rewrite b.txt.", "acceptance": "b.txt is new.", "needs": "write_local"},
		{"goal": "[[fork-3]] Also rewrite a.txt.", "acceptance": "a.txt is new.", "needs": "write_local"},
	}
	add("route-fork", fakemodel.Match{System: "You route one piece of work", User: "[[fork]]"}, fakemodel.Step{JSON: map[string]any{"step": "forked", "owner_bot_id": "", "parts": parts, "rationale": "three file edits", "confidence": 0.9}})
	report := fakemodel.Step{ToolCalls: call("report_result", map[string]any{"summary": "Rewrote the file.", "findings": []map[string]any{}})}
	add("h1", fakemodel.Match{System: "You are a helper", User: "[[fork-1]]"}, write("a.txt", "A from helper one"), report)
	add("h2", fakemodel.Match{System: "You are a helper", User: "[[fork-2]]"}, write("b.txt", "B from helper two"), report)
	add("h3", fakemodel.Match{System: "You are a helper", User: "[[fork-3]]"}, write("a.txt", "A from helper three"), report)
	add("resolve", fakemodel.Match{System: "[[script:writer]]", User: "Result of merging your helpers' files"}, write("a.txt", "A resolved by the owner"), fakemodel.Step{Text: "Merged the notes."})
	run, _ = e.Post(writer.ChatID, "[[fork]] Rewrite both notes in parallel.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	if d := routeOf(t, r); d.Step != "forked" {
		t.Fatalf("route %+v", d)
	}
	var merge struct {
		Applied   []string `json:"applied"`
		Conflicts []struct {
			Path     string `json:"path"`
			Versions []struct {
				Content string `json:"content"`
			} `json:"versions"`
		} `json:"conflicts"`
	}
	for _, ev := range r.Events {
		if ev.Type == "merge_result" {
			_ = json.Unmarshal(ev.Data, &merge)
		}
	}
	if len(merge.Applied) != 1 || merge.Applied[0] != "home/b.txt" || len(merge.Conflicts) != 1 || merge.Conflicts[0].Path != "home/a.txt" || len(merge.Conflicts[0].Versions) != 2 {
		t.Fatalf("merge %+v: %s", merge, r.EventTypes())
	}
	if r.Line.State != "done" {
		t.Fatalf("run %s: %s", r.Line.State, r.EventTypes())
	}
	read := func(name string) string {
		ctx := context.Background()
		l, err := e.App.Computers.Acquire(ctx, uuid.MustParse(zoneID(e, "personal")))
		if err != nil {
			t.Fatal(err)
		}
		defer l.Release()
		b, err := l.Client.ReadFile(ctx, computers.BotHome(uuid.MustParse(writer.ID))+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	if a, b := read("a.txt"), read("b.txt"); a != "A resolved by the owner" || b != "B from helper two" {
		t.Fatalf("workspace a=%q b=%q", a, b)
	}
}
