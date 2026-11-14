import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-UX-010 bot output renders raw HTML as text and keeps javascript links inactive", async ({ page }) => {
  const s = state();
  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);
  await signIn(page, `/chat/${chief.chat_id}`);
  await page.getByLabel(/Message to/).fill("[[unsafe-output]] show me");
  await page.keyboard.press("Enter");
  const reply = page.getByRole("article", { name: /Message from Chief/ }).last();
  await expect(reply).toContainText("<script>window.__fettleXSS = 1</script>");
  await expect(reply).toContainText('<img src=x onerror="window.__fettleXSS = 2">');
  expect(await page.evaluate(() => (window as unknown as { __fettleXSS?: number }).__fettleXSS)).toBeUndefined();
  await expect(reply.locator("script")).toHaveCount(0);
  await expect(reply.locator("img")).toHaveCount(0);
  const bad = reply.getByText("click me");
  await expect(bad).not.toHaveAttribute("href", /javascript/);
  await bad.click();
  expect(await page.evaluate(() => (window as unknown as { __fettleXSS?: number }).__fettleXSS)).toBeUndefined();
  await expect(reply.getByRole("link", { name: "docs" })).toHaveAttribute("href", "https://docs.test/");
});
