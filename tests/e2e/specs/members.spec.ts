import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures";
import { MEMBER, OWNER, startServerMode, type ServerMode } from "../server";

const OWNER_TABS = ["Models", "Computers", "Connectors", "Vault", "Approvals", "Budgets", "Cost", "Members", "Space", "Audit log"];
const MEMBER_TABS = ["Account", "Notifications", "Access tokens"];

let server: ServerMode;

test.beforeAll(async () => {
  server = await startServerMode();
});

test.afterAll(async () => {
  await server?.stop();
});

function settingsNav(page: Page) {
  return page.getByRole("navigation", { name: "Settings pages" });
}

test("SCN-UX-010 member routes exclude owner settings", async ({ browser }) => {
  test.setTimeout(120_000);

  // The owner signs in through the sign-in page and makes an invite link for a member.
  const ownerCtx = await browser.newContext({ baseURL: server.url });
  const owner = await ownerCtx.newPage();
  await owner.goto("/settings/members");
  await owner.getByLabel("Email").fill(OWNER.email);
  await owner.getByLabel("Password").fill(OWNER.password);
  await owner.getByRole("button", { name: "Sign in" }).click();
  for (const tab of [...OWNER_TABS, ...MEMBER_TABS, "Instance"]) {
    await expect(settingsNav(owner).getByRole("link", { name: tab, exact: true })).toBeVisible();
  }
  await owner.getByLabel("Role", { exact: true }).selectOption("member");
  await owner.getByRole("button", { name: "Make invite link" }).click();
  const link = owner.getByText(/\/invite\/inv_/);
  await expect(link).toBeVisible();
  const invite = (await link.textContent())!.trim();
  expect(invite.startsWith(`${server.url}/invite/`)).toBe(true);

  // The member makes an account through the invite link.
  const memberCtx = await browser.newContext({ baseURL: server.url });
  const member = await memberCtx.newPage();
  await member.goto(invite);
  await expect(member.getByRole("heading", { name: /^Join / })).toBeVisible();
  await member.getByLabel("Your name").fill(MEMBER.name);
  await member.getByLabel("Email").fill(MEMBER.email);
  await member.getByLabel("Password").fill(MEMBER.password);
  await member.getByRole("button", { name: "Create account and join" }).click();
  await expect(member).not.toHaveURL(/\/invite\//);

  // The member sees the account pages and no owner page.
  await member.goto("/settings/account");
  await expect(member.getByLabel("Email")).toHaveValue(MEMBER.email);
  const nav = settingsNav(member);
  for (const tab of MEMBER_TABS) await expect(nav.getByRole("link", { name: tab, exact: true })).toBeVisible();
  for (const tab of [...OWNER_TABS, "Instance"]) {
    await expect(nav.getByRole("link", { name: tab, exact: true })).toHaveCount(0);
  }

  await member.goto("/settings/models");
  await expect(member.getByText("Only an owner of this space can see this.")).toBeVisible();
  await expect(member.getByRole("heading", { name: "Model providers" })).toHaveCount(0);

  await member.goto("/admin");
  await expect(member.getByText("Only an instance admin can see this page.")).toBeVisible();
  await expect(member.getByRole("heading", { name: "Users" })).toHaveCount(0);

  // The owner sees the new member in the list.
  await owner.reload();
  await expect(owner.getByRole("list", { name: "Members of this space" })).toContainText(MEMBER.email);

  await memberCtx.close();
  await ownerCtx.close();
});
