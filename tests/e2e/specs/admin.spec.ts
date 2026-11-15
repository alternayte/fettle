import { expect, signIn, test } from "../fixtures";

const FEATURES = [
  "Single sign-on (SSO)",
  "User provisioning (SCIM)",
  "Audit log export",
  "Policy packs",
  "Organization budgets",
  "Admin console",
];

test("SCN-EE-001 the admin page shows Enterprise features as unavailable", async ({ page }) => {
  // The local owner is an instance admin, and the default build is the community edition.
  await signIn(page, "/admin");
  const licence = page.getByRole("region", { name: "Licence and Enterprise features" });
  await expect(licence.getByText("Community edition", { exact: true })).toBeVisible();
  await expect(licence.getByText("The Enterprise build of Fettle has these features.")).toBeVisible();
  const list = licence.getByRole("list", { name: "Enterprise features" });
  await expect(list.getByRole("listitem")).toHaveCount(FEATURES.length);
  for (const name of FEATURES) {
    const row = list.getByRole("listitem").filter({ hasText: name });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText("Not available");
  }
  await expect(list).not.toContainText(/\bAvailable\b|Read-only/);

  const instance = page.getByRole("region", { name: "Instance" });
  await expect(instance).toContainText("Community edition");
  await expect(instance).toContainText("Local");
});
