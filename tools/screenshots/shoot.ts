// shoot.ts makes the product screenshots in docs/images. It starts the fake model, two
// fixture sites and a Fettle server with a scratch data folder, seeds the synthetic Acme
// space through the API, and captures each page with Playwright at 1440x900, scale 2.
//
// Run it with `just screenshots`. Pass --keep to leave the server running after the
// capture; the script then prints the URL with the local token. Pass --no-capture to only
// seed, and --only=name,name to capture some of the shots.
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { createServer } from "node:net";
import { join, resolve } from "node:path";
import { chromium, type Browser, type Page } from "playwright";

const ROOT = resolve(import.meta.dir, "../..");
const OUT = join(ROOT, "docs/images");
const IMAGE = process.env.FETTLE_COMPUTER_IMAGE ?? "fettle-computer:dev";
const KEEP = process.argv.includes("--keep");
const NO_CAPTURE = process.argv.includes("--no-capture");
const ONLY = process.argv.find((a) => a.startsWith("--only="))?.slice(7).split(",");
const VIEW = { width: 1440, height: 900 };

const procs: ChildProcess[] = [];
const containers: string[] = [];
let zoneIDs: string[] = [];
let dataDir = "";

function bin(p: string): string {
  const full = join(ROOT, p);
  if (!existsSync(full)) throw new Error(`${p} is missing: run just build and the fakes builds of just screenshots`);
  return full;
}

function freePort(): Promise<number> {
  return new Promise((res, rej) => {
    const s = createServer();
    s.once("error", rej);
    s.listen(0, "127.0.0.1", () => {
      const a = s.address();
      s.close(() => (a && typeof a === "object" ? res(a.port) : rej(new Error("no port"))));
    });
  });
}

function waitLine(p: ChildProcess, re: RegExp, ms: number): Promise<string> {
  return new Promise((res, rej) => {
    let log = "";
    const timer = setTimeout(() => rej(new Error(`timeout waiting for ${re}:\n${log}`)), ms);
    const on = (b: Buffer) => {
      log += b.toString();
      const m = log.match(re);
      if (m) {
        clearTimeout(timer);
        res(m[1] ?? m[0]);
      }
    };
    p.stdout?.on("data", on);
    p.stderr?.on("data", on);
  });
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function until<T>(what: string, fn: () => Promise<T | undefined | null | false>, ms = 120_000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timeout: ${what}`);
    await sleep(500);
  }
}

function cleanup() {
  for (const p of procs) {
    try {
      p.kill("SIGTERM");
    } catch {
      // The process already ended.
    }
  }
  // Only the fixture sites and the computers of this space. Other servers keep theirs.
  const ids = [...containers];
  for (const z of zoneIDs) {
    try {
      ids.push(...execFileSync("docker", ["ps", "-aq", "--filter", `label=fettle.zone=${z}`]).toString().split(/\s+/).filter(Boolean));
    } catch {
      // Docker is not reachable.
    }
  }
  for (const id of ids) {
    try {
      execFileSync("docker", ["rm", "-f", id], { stdio: "ignore" });
    } catch {
      // The container already ended.
    }
  }
  if (dataDir) rmSync(dataDir, { recursive: true, force: true });
}

async function start() {
  const fettle = bin("bin/fettle");
  const fakes = bin("bin/fettle-fakes");
  bin("bin/linux/fettle-fakes");

  const fake = spawn(fakes, ["fake", "serve", "--model-addr", "127.0.0.1:0", "--scripts", join(import.meta.dir, "scripts")], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  procs.push(fake);
  const fakeURL = await waitLine(fake, /FAKE_MODEL_URL=(\S+)/, 20_000);

  const hosts: string[] = [];
  for (const site of ["docs.test", "shop.test"]) {
    const id = execFileSync("docker", ["run", "-d", "--rm", "--user", "0", "--entrypoint", "/fakes/fettle-fakes", "-v", `${join(ROOT, "bin/linux")}:/fakes:ro`, IMAGE, "fake", "serve", "--web-addr", ":80"])
      .toString()
      .trim();
    containers.push(id);
    const ip = execFileSync("docker", ["inspect", "-f", "{{.NetworkSettings.IPAddress}}", id]).toString().trim();
    hosts.push(`${site}=${ip}`);
  }

  // A fixed, neutral path, because the diagnostics shot shows the database path.
  dataDir = "/tmp/fettle-screenshots";
  rmSync(dataDir, { recursive: true, force: true });
  mkdirSync(dataDir, { recursive: true });
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const server = spawn(fettle, ["serve", "--data", dataDir, "--addr", `127.0.0.1:${port}`], {
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      FETTLE_NO_KEYCHAIN: "1",
      FETTLE_LOG_LEVEL: "info",
      FETTLE_DEFAULT_PROVIDER: "docker",
      FETTLE_COMPUTER_IMAGE: IMAGE,
      FETTLE_COMPUTER_HOSTS: hosts.join(","),
    },
  });
  procs.push(server);
  await waitLine(server, /fettle is ready/, 60_000);
  const token = readFileSync(join(dataDir, "local.token"), "utf8").trim();
  return { url, token, fakeURL };
}

type Api = (method: string, path: string, body?: unknown) => Promise<any>; // eslint-disable-line @typescript-eslint/no-explicit-any

function client(url: string, token: string): Api {
  return async (method, path, body) => {
    const r = await fetch(`${url}/api/v1${path}`, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${await r.text()}`);
    return r.status === 204 ? null : r.json();
  };
}

type Seed = {
  space: string;
  bots: Record<string, { id: string; chat_id: string }>;
  group: string;
  fanoutRun: string;
};

async function seed(api: Api, fakeURL: string): Promise<Seed> {
  const me = await api("GET", "/me");
  const space = me.spaces[0].id as string;
  const S = `/spaces/${space}`;
  await api("PATCH", "/me", { name: "Ada" });
  await api("PATCH", S, { name: "Acme", time_zone: "UTC" });

  const p = await api("POST", `${S}/model-providers`, { name: "Acme models", api_type: "openai-chat", base_url: fakeURL, key: "sk-acme-shots-0001" });
  await api("POST", `${S}/model-providers/${p.id}/test`, { model: "acme/large" });
  for (const role of ["owner", "summarizer"]) {
    await api("PUT", `${S}/model-roles/${role}`, { targets: [{ provider_id: p.id, model: "acme/large", price_in: 3, price_out: 15, context_window: 128000 }] });
  }

  for (const t of ["research", "inbox", "weekly-report", "pr-reviewer"]) await api("POST", `${S}/templates/${t}`);
  type Zone = { id: string; name: string; network: string; allow_hosts: string[] | null; idle_minutes: number; share_blocked: boolean; provider_id: string };
  const zones = (await api("GET", `${S}/zones`)).zones as Zone[];
  zoneIDs = zones.map((z) => z.id);
  // Desktop zones have a screen, so the screen page shows the browser of a computer.
  for (const z of zones) {
    await api("PATCH", `${S}/zones/${z.id}`, { name: z.name, network: z.network, allow_hosts: z.allow_hosts, idle_minutes: z.idle_minutes, share_blocked: z.share_blocked, provider_id: z.provider_id, size: "desktop" });
  }
  const personal = zones.find((z) => /personal/i.test(z.name)) ?? zones[0];
  await api("POST", `${S}/bots`, {
    name: "Ledger",
    job: "Checks supplier invoices and tracks the budgets",
    public_desc: "Checks supplier invoices, tracks open payments and the budgets of the space",
    instructions: "You check supplier invoices against the orders.\n- Give each amount in USD.\n- Say which invoices are open and when they are due.",
    zone_id: personal.id,
  });

  const list = (await api("GET", `${S}/bots`)).bots as { id: string; name: string; chat_id: string; is_primary: boolean }[];
  const bots: Seed["bots"] = {};
  for (const b of list) bots[b.is_primary ? "Chief" : b.name] = { id: b.id, chat_id: b.chat_id };
  for (const n of ["Chief", "Research", "Inbox", "Reporter", "Reviewer", "Ledger"]) if (!bots[n]) throw new Error(`no bot ${n}: ${list.map((b) => b.name)}`);

  type Line = { run_id: string; label: string; state: string };
  const settle = async (chat: string, label: string, states: string[]) =>
    until(`run "${label}" in ${states}`, async () => {
      const page = await api("GET", `${S}/chats/${chat}/messages`);
      const r = ((page.runs ?? []) as Line[]).find((x) => x.label.includes(label));
      if (r && ["failed", "escalated", "cancelled"].includes(r.state) && !states.includes(r.state)) throw new Error(`run "${label}" is ${r.state}`);
      return r && states.includes(r.state) ? r : undefined;
    }, 240_000);
  const say = async (chat: string, body: string) => api("POST", `${S}/chats/${chat}/messages`, { body });

  // The direct chat of Chief: a fanout over three catalogue pages, verified.
  await say(bots.Chief.chat_id, "Compare the three desks in the catalogue at docs.test and pick one for the new office.");
  const fan = await settle(bots.Chief.chat_id, "Compare the three desks", ["done"]);

  // A group chat about the office move.
  const group = await api("POST", `${S}/chats`, { name: "Office move", bot_ids: [bots.Chief.id, bots.Research.id, bots.Ledger.id, bots.Reporter.id] });
  await say(group.id, "@Research what do the desks weigh? The lift in the new building takes 35 kg for each item.");
  await settle(group.id, "what do the desks weigh", ["done"]);
  await say(group.id, "@Ledger how much of the furniture budget is left this quarter? We want twelve Gamma Desks.");
  await settle(group.id, "furniture budget", ["done"]);
  await say(group.id, "@Reporter add the desk plan to the weekly report, please.");
  await settle(group.id, "add the desk plan", ["done"]);

  // Chief waits for an approval; Inbox waits for an answer.
  await say(bots.Chief.chat_id, "Good. Send an order note for eight Gamma Desks to the shop on its actions page.");
  await settle(bots.Chief.chat_id, "Send an order note", ["waiting"]);
  await say(bots.Inbox.chat_id, "Draft a reply to Birchline Furniture about the late chairs.");
  await settle(bots.Inbox.chat_id, "Draft a reply to Birchline", ["waiting"]);

  // A computer that stops during the seed loses an attachment; fail rather than show it.
  const files = (await api("GET", `${S}/files`)).files as unknown[];
  if (files.length !== 2) throw new Error(`want 2 files in the space, got ${files.length}`);

  return { space, bots, group: group.id, fanoutRun: fan.run_id };
}

async function main() {
  mkdirSync(OUT, { recursive: true });
  const { url, token, fakeURL } = await start();
  const api = client(url, token);
  const s = await seed(api, fakeURL);
  console.log(JSON.stringify(s, null, 2));
  if (!NO_CAPTURE) {
    const browser = await chromium.launch();
    try {
      await capture(browser, url, token, s);
    } finally {
      await browser.close();
    }
  }
  if (KEEP) {
    console.log(`${url}/#token=${token}`);
    console.log("Press Ctrl-C to stop.");
    await new Promise(() => {});
  }
}

type Shot = { name: string; path: string; theme?: "light" | "dark"; ready: (p: Page) => Promise<unknown>; act?: (p: Page) => Promise<unknown> };

function shots(s: Seed): Shot[] {
  const text = (t: string | RegExp) => (p: Page) => p.getByText(t).first().waitFor({ timeout: 60_000 });
  return [
    // First, while the computer of the zone is fresh.
    {
      name: "computer-screen",
      path: `/screen/${s.bots.Research.id}`,
      ready: (p) => p.locator("canvas").first().waitFor(),
      act: async (p) => {
        await sleep(4000);
        if (await p.getByText("The screen connection ended.").isVisible()) throw new Error("the screen connection ended");
      },
    },
    { name: "chat-approval", path: `/chat/${s.bots.Chief.chat_id}`, ready: (p) => p.getByRole("button", { name: "Allow once" }).waitFor() },
    { name: "chat-approval-dark", path: `/chat/${s.bots.Chief.chat_id}`, theme: "dark", ready: (p) => p.getByRole("button", { name: "Allow once" }).waitFor() },
    { name: "group-chat", path: `/chat/${s.group}`, ready: text("I added an") },
    {
      name: "run-timeline",
      path: `/runs/${s.fanoutRun}`,
      ready: text("Helper result"),
      act: (p) => p.getByRole("button", { name: /^helper result read http:\/\/docs\.test\/beta/i }).click(),
    },
    { name: "bots", path: "/bots", ready: text("Waiting for you") },
    { name: "files", path: "/files", ready: text("open-invoices.csv") },
    { name: "settings-computers", path: "/settings/computers", ready: text("Running") },
    {
      name: "admin-diagnostics",
      path: "/admin",
      ready: text("Community edition"),
      act: async (p) => {
        await p.getByRole("button", { name: "Run checks" }).click();
        await p.getByText(/of \d+ checks pass/).waitFor({ timeout: 60_000 });
        await p.locator("#diagnostics-section").evaluate((e) => e.scrollIntoView());
      },
    },
  ];
}

async function capture(browser: Browser, url: string, token: string, s: Seed) {
  for (const shot of shots(s)) {
    if (ONLY && !ONLY.includes(shot.name)) continue;
    const theme = shot.theme ?? "light";
    const ctx = await browser.newContext({ baseURL: url, viewport: VIEW, deviceScaleFactor: 2, colorScheme: theme, reducedMotion: "reduce", locale: "en-GB", timezoneId: "UTC" });
    await ctx.addInitScript((t) => localStorage.setItem("fettle.theme", t), theme);
    const page = await ctx.newPage();
    await page.goto(`${shot.path}#token=${token}`);
    await shot.ready(page);
    await page.addStyleTag({ content: "*, *::before, *::after { caret-color: transparent !important; transition: none !important; animation: none !important; }" });
    await shot.act?.(page);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.mouse.move(VIEW.width - 2, VIEW.height - 2);
    await sleep(800);
    const file = join(OUT, `${shot.name}.png`);
    await page.screenshot({ path: file });
    await ctx.close();
    execFileSync("pngquant", ["--force", "--strip", "--quality", "60-95", "--output", file, file]);
    execFileSync("oxipng", ["-q", "-o", "4", "--strip", "safe", file]);
    console.log(`${shot.name}.png ${Math.round(statSync(file).size / 1024)} KB`);
  }
}

for (const sig of ["SIGINT", "SIGTERM"] as const) {
  process.on(sig, () => {
    cleanup();
    process.exit(130);
  });
}
main()
  .then(() => cleanup())
  .catch((e) => {
    console.error(e);
    cleanup();
    process.exit(1);
  });

