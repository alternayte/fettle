import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-BOT-005 a fanout envelope shows one run line with helpers, time, cost and checks, and it opens the timeline", async ({
  page,
}) => {
  const s = state();
  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);
  await signIn(page, `/chat/${chief.chat_id}`);
  await page
    .getByLabel(/Message to/)
    .fill("[[fanline]] Compare the alpha and beta desks.");
  await page.getByLabel(/Message to/).press("Enter");

  const line = page.getByRole("button", {
    name: /Open the timeline of \[\[fanline\]\]/,
  });
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("2 helpers", { timeout: 60_000 });
  await expect(line).toContainText("Verified", { timeout: 60_000 });
  await expect(line).toContainText(/\d+ s|\d+ min/);
  await expect(line).toContainText("$");
  await expect(page.getByRole("table")).toContainText("Beta Desk");

  await line.click();
  await expect(
    page.getByText("Helpers", { exact: true }).first()
  ).toBeVisible();
});
