import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-VLT-002 the eye toggle shows a value before save and the page never holds it after", async ({ page }) => {
  const secret = "pw-correct-horse-9876";
  await signIn(page, "/settings/vault");
  await page.getByRole("button", { name: "Add an item" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Shop login");
  await page.getByLabel("Site", { exact: true }).fill("https://shop.test");
  await page.getByLabel("User name").fill("owner@acme.test");
  const value = page.getByLabel("Password", { exact: true });
  await value.fill(secret);
  await expect(value).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "Show the value" }).click();
  await expect(value).toHaveAttribute("type", "text");
  await expect(value).toHaveValue(secret);
  await page.getByRole("button", { name: /^Save/ }).click();
  await expect(page.getByText("•••• 9876").first()).toBeVisible();
  await page.reload();
  await expect(page.getByText("•••• 9876").first()).toBeVisible();
  const html = await page.content();
  expect(html).not.toContain(secret);
  const items = await api("GET", `/spaces/${state().space}/vault`);
  expect(JSON.stringify(items)).not.toContain(secret);
});
