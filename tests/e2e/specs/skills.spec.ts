import { readFileSync } from "node:fs";
import { api, expect, signIn, state, test } from "../fixtures";

async function waitDone(space: string, label: string): Promise<string> {
  for (let i = 0; i < 100; i++) {
    const runs = await api("GET", `/spaces/${space}/runs`);
    const r = runs.runs.find((x: { line: { label: string } }) =>
      x.line.label.includes(label)
    );
    if (r?.line.state === "done") return r.line.run_id;
    await new Promise((f) => setTimeout(f, 200));
  }
  throw new Error(`no finished run for ${label}`);
}

test("SCN-SKL-003 a skill zip round-trips, and Save as skill opens a draft with the steps of the run", async ({
  page,
}) => {
  const s = state();
  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);

  await signIn(page, "/skills");
  await page.getByRole("button", { name: "New skill" }).click();
  await page
    .getByLabel("SKILL.md")
    .fill(
      "---\nname: acme-triage\ndescription: Sort the Acme inbox by urgency.\n---\n\n1. Read the inbox.\n"
    );
  await page.getByRole("button", { name: "Save skill" }).click();
  await page.getByRole("link", { name: /acme-triage/ }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "acme-triage" })
  ).toBeVisible();

  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Export zip" }).click();
  const zip = readFileSync(await (await download).path());
  await page.goto("/skills");
  await page
    .locator('input[type="file"]')
    .setInputFiles({
      name: "acme-triage.zip",
      mimeType: "application/zip",
      buffer: zip,
    });
  await expect(
    page.getByRole("heading", { level: 1, name: "acme-triage" })
  ).toBeVisible();
  await expect(
    page.getByText("Imported acme-triage, version 1.")
  ).toBeVisible();

  await api("POST", `/spaces/${s.space}/chats/${chief.chat_id}/messages`, {
    body: "[[skill-steps]] find the open invoices",
  });
  const run = await waitDone(s.space, "find the open invoices");
  await page.goto(`/runs/${run}`);
  await page.getByRole("button", { name: "Save as skill" }).click();
  const draft = page.getByLabel("Draft SKILL.md");
  await expect(draft).toHaveValue(/## Steps/);
  await expect(draft).toHaveValue(/search_history/);
  await page.getByRole("button", { name: "Save skill" }).click();
  await expect(page).toHaveURL(/\/skills\/[0-9a-f-]+$/);
});
