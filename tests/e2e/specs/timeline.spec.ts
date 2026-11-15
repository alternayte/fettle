import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-UX-011 a timeline of 5,000 events shows in 2 seconds and renders only the rows in view", async ({
  page,
}) => {
  test.setTimeout(420_000);
  const s = state();
  const set = (key: string, value: number) =>
    api("PUT", `/spaces/${s.space}/settings/${key}`, { value });
  // A long run needs room: turns and budgets above what 2,500 cheap turns use.
  await set("agent.max_turns", 2500);
  await set("budget.envelope_usd", 1000);
  await set("budget.bot_day_usd", 1000);
  await set("budget.space_month_usd", 10000);
  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);
  const posted = await api(
    "POST",
    `/spaces/${s.space}/chats/${chief.chat_id}/messages`,
    {
      body: "[[many]] Read the history.",
    }
  );
  const runID = posted.run_id as string;
  let events = 0;
  for (let i = 0; i < 600; i++) {
    const r = await api("GET", `/spaces/${s.space}/runs/${runID}`);
    events = r.events.length;
    if (r.line.state === "escalated" || r.line.state === "failed") break;
    await new Promise((f) => setTimeout(f, 500));
  }
  await set("agent.max_turns", 200);
  await set("budget.envelope_usd", 2);
  await set("budget.bot_day_usd", 10);
  await set("budget.space_month_usd", 100);
  expect(events).toBeGreaterThanOrEqual(5000);

  await signIn(page, "/");
  const start = Date.now();
  await page.goto(`/runs/${runID}`);
  const list = page.getByRole("list", { name: "Run events" });
  await expect(list.getByRole("listitem").first()).toBeVisible();
  expect(Date.now() - start).toBeLessThanOrEqual(2000);
  // The list is virtualized: the page holds the rows in view, not 5,000.
  expect(await list.getByRole("listitem").count()).toBeLessThan(200);
  await list
    .locator("xpath=..")
    .evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await expect(list.getByRole("listitem").last()).toBeVisible();

  // Stop the run so its card does not stay for other specs.
  const msgs = await api(
    "GET",
    `/spaces/${s.space}/chats/${chief.chat_id}/messages?limit=200`
  );
  for (const m of msgs.messages) {
    if (m.card && m.card.status === "pending") {
      await api("POST", `/spaces/${s.space}/cards/${m.card.id}/answer`, {
        action: "stop",
      });
    }
  }
});
