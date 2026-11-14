import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-BOT-004 a reply streams, its approval card does not block the chat, and the first token shows in 300 ms", async ({
  page,
}) => {
  const s = state();
  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);
  await signIn(page, `/chat/${chief.chat_id}`);
  await page.getByLabel(/Message to/).waitFor();
  // Record when the first streamed text shows in the page.
  await page.evaluate(() => {
    const w = window as unknown as {
      __firstText?: number;
      __lengths: number[];
    };
    w.__lengths = [];
    new MutationObserver(() => {
      const last = [
        ...document.querySelectorAll(
          'article[aria-label^="Message from Chief"]',
        ),
      ].pop();
      const n = last?.textContent?.includes("I open the actions page")
        ? (last.textContent?.length ?? 0)
        : 0;
      if (n > 0) {
        w.__firstText ??= Date.now();
        if (w.__lengths.at(-1) !== n) w.__lengths.push(n);
      }
    }).observe(document.body, {
      subtree: true,
      childList: true,
      characterData: true,
    });
  });
  const before = Date.now();
  await page
    .getByLabel(/Message to/)
    .fill("[[stream-card]] press Send on the shop actions page");
  await page.keyboard.press("Enter");

  const card = page.getByRole("region", { name: "Send a message" });
  await expect(card).toBeVisible({ timeout: 60_000 });
  const seen = await page.evaluate(() => {
    const w = window as unknown as {
      __firstText?: number;
      __lengths: number[];
    };
    return { __firstText: w.__firstText, __lengths: w.__lengths };
  });
  // Tokens appeared as they arrived: the text grew in several steps.
  expect(seen.__lengths.length).toBeGreaterThan(3);
  const firsts = (
    await (await fetch(`${s.fakeURL}/__fake/first-tokens`)).json()
  ).first_tokens_ms as number[];
  const fakeFirst = firsts
    .filter((ms) => ms >= before)
    .sort((a, b) => a - b)[0];
  expect(seen.__firstText! - fakeFirst).toBeLessThanOrEqual(300);

  // The chat takes another message while the card is open.
  await page
    .getByLabel(/Message to/)
    .fill("[[note]] also look at the prices later");
  await page.keyboard.press("Enter");
  await expect(page.getByText("also look at the prices later")).toBeVisible();
  await expect(card.getByRole("button", { name: "Allow once" })).toBeVisible();
  await card.getByRole("button", { name: "Deny" }).click();
  await expect(page.getByText("· Denied")).toBeVisible();
});
