import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { gzipSync } from "node:zlib";
import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { api, expect, signIn, state, test } from "../fixtures";

const WIDTHS = [
  { name: "phone", width: 360, height: 780 },
  { name: "desktop", width: 1280, height: 860 },
];
const THEMES = ["light", "dark"] as const;

async function routes(): Promise<string[]> {
  const s = state();
  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);
  const runs = await api("GET", `/spaces/${s.space}/runs`);
  const list = [
    "/",
    `/chat/${chief.chat_id}`,
    "/runs",
    "/routines",
    "/skills",
    `/bots/${chief.id}`,
    "/settings/models",
    "/settings/computers",
    "/settings/approvals",
    "/settings/connectors",
    "/settings/vault",
    "/settings/usage",
  ];
  if (runs.runs?.length) list.push(`/runs/${runs.runs[0].line.run_id}`);
  return list;
}

async function settle(page: Page) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await expect(page.locator("#root")).not.toBeEmpty();
}

test("SCN-UX-008 main routes have no serious axe violations and no horizontal scroll at 360 px and desktop width, light and dark", async ({
  page,
}) => {
  test.setTimeout(300_000);
  const paths = await routes();
  await signIn(page, "/");
  await settle(page);
  const problems: string[] = [];
  for (const theme of THEMES) {
    await page.evaluate((t) => localStorage.setItem("fettle.theme", t), theme);
    for (const size of WIDTHS) {
      await page.setViewportSize({ width: size.width, height: size.height });
      for (const path of paths) {
        await page.goto(path);
        await settle(page);
        expect(
          await page.evaluate(() => document.documentElement.dataset.theme)
        ).toBe(theme);
        const results = await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze();
        for (const v of results.violations) {
          if (v.impact === "serious" || v.impact === "critical") {
            problems.push(
              `${theme} ${size.name} ${path}: ${v.id} (${v.impact}) ${v.nodes
                .slice(0, 3)
                .map((n) => n.target.join(" "))
                .join(" | ")}`
            );
          }
        }
        const overflow = await page.evaluate(() => {
          const el = document.scrollingElement ?? document.documentElement;
          return el.scrollWidth - el.clientWidth;
        });
        if (overflow > 0)
          problems.push(
            `${theme} ${size.name} ${path}: horizontal scroll of ${overflow} px`
          );
      }
    }
  }
  await page.evaluate(() => localStorage.removeItem("fettle.theme"));
  expect(problems, problems.join("\n")).toEqual([]);
});

test("SCN-UX-008 Cmd+K opens the command menu", async ({ page }) => {
  await signIn(page, "/");
  await settle(page);
  await page.keyboard.press(
    process.platform === "darwin" ? "Meta+k" : "Control+k"
  );
  const dialog = page.getByRole("dialog", { name: "Command menu" });
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByPlaceholder("Type a bot, a page or an action")
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("SCN-UX-008 reduced motion turns animations and transitions off", async ({
  browser,
}) => {
  const ctx = await browser.newContext({ reducedMotion: "reduce" });
  const page = await ctx.newPage();
  await signIn(page, "/");
  await settle(page);
  const durations = await page.evaluate(() => {
    const out: string[] = [];
    for (const el of Array.from(document.querySelectorAll("body *")).slice(
      0,
      400
    )) {
      const cs = getComputedStyle(el);
      for (const d of [
        ...cs.transitionDuration.split(","),
        ...cs.animationDuration.split(","),
      ]) {
        const ms = d.trim().endsWith("ms")
          ? parseFloat(d)
          : parseFloat(d) * 1000;
        if (ms > 1) out.push(`${el.tagName}.${el.className}: ${d.trim()}`);
      }
    }
    return out;
  });
  expect(durations, durations.slice(0, 10).join("\n")).toEqual([]);
  await ctx.close();
});

test("SCN-UX-008 first paint is 1.5 s or less and the initial bundle is 600 KB gzip or less", async ({
  page,
}) => {
  await signIn(page, "/");
  await settle(page);
  await page.goto("/");
  const fcp = await page.evaluate(
    () =>
      new Promise<number>((resolve) => {
        const now = performance.getEntriesByName("first-contentful-paint")[0];
        if (now) return resolve(now.startTime);
        new PerformanceObserver((l) => {
          const e = l.getEntriesByName("first-contentful-paint")[0];
          if (e) resolve(e.startTime);
        }).observe({ type: "paint", buffered: true });
      })
  );
  expect(fcp).toBeLessThanOrEqual(1500);

  // The initial route loads the entry script, its preloads and the CSS from index.html.
  const dist = resolve("../../ui/dist");
  const html = readFileSync(join(dist, "index.html"), "utf8");
  const assets = [
    ...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g),
  ].map((m) => m[1]);
  expect(assets.length).toBeGreaterThan(0);
  expect(readdirSync(join(dist, "assets")).length).toBeGreaterThan(0);
  let gz = 0;
  for (const a of new Set(assets))
    gz += gzipSync(readFileSync(join(dist, a))).length;
  console.log(
    `first paint ${Math.round(fcp)} ms; initial bundle ${(gz / 1024).toFixed(
      1
    )} KB gzip`
  );
  expect(gz).toBeLessThanOrEqual(600 * 1024);
});
