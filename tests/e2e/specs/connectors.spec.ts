import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-CON-005 the Connect screen shows the catalogue and the test lists tools with classes", async ({ page }) => {
  const s = state();
  const catalog = await api("GET", "/connectors/catalog");
  await signIn(page, "/settings/connectors");
  const list = page.getByRole("list", { name: "Connect an app" });
  for (const entry of catalog.connectors) await expect(list.getByText(entry.name, { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Any MCP server" }).click();
  await page.getByLabel("Name", { exact: true }).fill("fakemail");
  await page.getByLabel("MCP URL").fill(s.mcpURL);
  await page.getByLabel("Sign-in").selectOption("header");
  await page.getByLabel("Header name").fill("X-Api-Key");
  await page.getByRole("button", { name: "Add connector" }).click();

  await page.getByRole("button", { name: /fakemail/ }).click();
  await page.getByLabel("Account name").fill("work");
  await page.getByLabel("Key", { exact: true }).fill("acme-mail-key-9901");
  await page.getByRole("button", { name: "Add account" }).click();
  await page.getByRole("button", { name: "Test" }).click();
  const tools = page.getByRole("list", { name: "Tools" });
  const cls = (tool: string) => tools.getByRole("listitem").filter({ hasText: tool }).getByRole("combobox");
  await expect(cls("list_inbox")).toHaveValue("read");
  await expect(cls("send_email")).toHaveValue("send");
  await expect(cls("delete_email")).toHaveValue("write_external");
});
