import {
  ApiError,
  claim,
  loadConnection,
  normalizeServer,
  pairingFromURL,
  saveConnection,
  share,
  targets,
  type Connection,
  type SessionCookie,
  type Targets,
} from "./api";

const root = document.getElementById("app") as HTMLElement;

/** el builds an element with text; text never becomes markup. */
function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  props: Record<string, unknown> = {},
  ...kids: (Node | string)[]
): HTMLElementTagNameMap[K] {
  const e = Object.assign(document.createElement(tag), props);
  for (const k of kids) e.append(k);
  return e;
}

function show(...nodes: Node[]) {
  root.replaceChildren(...nodes);
}

function message(title: string, body?: string, tone: "plain" | "error" = "plain") {
  return el("div", { className: `note ${tone}` }, el("p", { className: "strong" }, title), ...(body ? [el("p", {}, body)] : []));
}

const AGAIN = "In Fettle, open Settings, Access tokens, and click Connect Chrome. Then try again.";

async function activeTab(): Promise<chrome.tabs.Tab | undefined> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab;
}

function footer(c: Connection) {
  const out = el("button", { type: "button", className: "link" }, "Disconnect");
  out.addEventListener("click", async () => {
    await saveConnection(null);
    void start();
  });
  return el("footer", {}, el("span", {}, `Connected to ${c.server} as ${c.account}.`), " ", out);
}

// ---------------------------------------------------------------- pairing

function pairView(server: string, code: string) {
  const btn = el("button", { type: "button", className: "primary" }, "Connect");
  const status = el("div");
  btn.addEventListener("click", async () => {
    btn.disabled = true;
    try {
      await saveConnection(await claim(server, code));
      show(message("Chrome is connected to Fettle.", "Open a site where you are signed in. Then click the Fettle icon to share the sign-in with a bot."));
    } catch (err) {
      btn.disabled = false;
      status.replaceChildren(message("Fettle did not connect.", (err as Error).message, "error"));
    }
  });
  show(el("h1", {}, "Connect Chrome to Fettle"), el("p", {}, `Server: ${server}`), btn, status);
}

function manualPairView() {
  const server = el("input", { id: "server", value: "http://127.0.0.1:7420", autocomplete: "off" });
  const code = el("input", { id: "code", placeholder: "ABCD-EFGH-JKLM", autocomplete: "off" });
  const btn = el("button", { type: "submit", className: "primary" }, "Connect");
  const status = el("div");
  const form = el(
    "form",
    {},
    el("label", { htmlFor: "server" }, "Server"),
    server,
    el("label", { htmlFor: "code" }, "Code"),
    code,
    btn,
  );
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    btn.disabled = true;
    try {
      const s = normalizeServer(server.value);
      await saveConnection(await claim(s, code.value));
      void start();
    } catch (err) {
      btn.disabled = false;
      status.replaceChildren(message("Fettle did not connect.", (err as Error).message, "error"));
    }
  });
  show(
    el("h1", {}, "Connect Chrome to Fettle"),
    el("p", {}, "In Fettle, open Settings, Access tokens, and click Connect Chrome. Then type the server and the code here."),
    form,
    status,
  );
}

// ---------------------------------------------------------------- share

function siteOrigins(site: string): string[] {
  const ip = /^[\d.]+$/.test(site) || site.includes(":") || !site.includes(".");
  const hosts = ip ? [site] : [site, `*.${site}`];
  return hosts.flatMap((h) => [`https://${h}/*`, `http://${h}/*`]);
}

function toCookie(c: chrome.cookies.Cookie): SessionCookie {
  return {
    name: c.name,
    value: c.value,
    domain: c.domain,
    path: c.path,
    host_only: c.hostOnly,
    secure: c.secure,
    http_only: c.httpOnly,
    same_site: c.sameSite,
    expires: c.session ? undefined : c.expirationDate,
  };
}

async function localStorageOf(tabId: number): Promise<Record<string, string>> {
  try {
    const [res] = await chrome.scripting.executeScript({
      target: { tabId },
      func: () => {
        const o: Record<string, string> = {};
        for (let i = 0; i < localStorage.length && i < 400; i++) {
          const k = localStorage.key(i);
          if (k !== null) o[k] = localStorage.getItem(k) ?? "";
        }
        return o;
      },
    });
    return (res?.result as Record<string, string> | undefined) ?? {};
  } catch {
    return {};
  }
}

function shareView(c: Connection, tab: chrome.tabs.Tab, t: Targets) {
  const site = t.site!;
  const host = new URL(tab.url!).hostname;
  const choices = t.spaces.flatMap((s) => s.bots.map((b) => ({ space: s, bot: b })));
  if (choices.length === 0) {
    show(
      message("No bot can take a sign-in.", "A bot needs a computer with a browser. In Fettle, set the size of its zone to browser or desktop."),
      footer(c),
    );
    return;
  }
  const asked = choices.findIndex((x) => x.bot.requests.some((r) => r === host || r === site || r.endsWith("." + site)));
  const select = el("select", { id: "bot" });
  choices.forEach((x, i) => {
    const label = t.spaces.length > 1 ? `${x.bot.name} · ${x.space.name}` : x.bot.name;
    select.append(el("option", { value: String(i), selected: i === Math.max(asked, 0) }, x.bot.requests.length ? `${label} (asks for a sign-in)` : label));
  });
  const who = el("p", { className: "soft" });
  const update = () => {
    const x = choices[Number(select.value)];
    who.textContent =
      x.space.people > 1
        ? `${x.space.people} people in ${x.space.name} can ask ${x.bot.name} to act with your sign-in.`
        : `Only you can ask ${x.bot.name} to act with your sign-in.`;
  };
  select.addEventListener("change", update);
  update();
  const btn = el("button", { type: "button", className: "primary" }, "Share sign-in");
  const status = el("div");
  btn.addEventListener("click", async () => {
    const x = choices[Number(select.value)];
    btn.disabled = true;
    // Chrome asks for access to this site only. The request must come first in the click.
    const granted = await chrome.permissions.request({ origins: siteOrigins(site) });
    if (!granted) {
      btn.disabled = false;
      status.replaceChildren(message("Chrome did not give access to the site.", "Fettle needs access to read your sign-in for " + site + "."));
      return;
    }
    try {
      const cookies = (await chrome.cookies.getAll({ domain: site })).map(toCookie);
      const storage = await localStorageOf(tab.id!);
      if (cookies.length === 0 && Object.keys(storage).length === 0) {
        throw new Error(`You are not signed in to ${site} in Chrome. Sign in first.`);
      }
      const r = await share(c, x.space.id, x.bot.id, { url: tab.url!, cookies, storage });
      show(
        message(
          `${x.bot.name} is signed in to ${r.site}.`,
          r.continued ? `${x.bot.name} continues its work.` : `${x.bot.name} uses the sign-in the next time it opens ${r.site}.`,
        ),
        footer(c),
      );
    } catch (err) {
      btn.disabled = false;
      if (err instanceof ApiError && err.status === 401) {
        await saveConnection(null);
        status.replaceChildren(message("Connect Chrome again.", AGAIN, "error"));
        return;
      }
      status.replaceChildren(message("The sign-in was not shared.", (err as Error).message, "error"));
    }
  });
  show(
    el("h1", {}, `Share your sign-in for ${site}`),
    el("p", {}, "The bot gets the cookies and the local storage of this site. Fettle keeps no copy."),
    el("label", { htmlFor: "bot" }, "Bot"),
    select,
    who,
    btn,
    status,
    footer(c),
  );
}

async function start() {
  const tab = await activeTab();
  const pairing = pairingFromURL(tab?.url);
  if (pairing) return pairView(pairing.server, pairing.code);
  const c = await loadConnection();
  if (!c) return manualPairView();
  if (!tab?.url || !/^https?:/.test(tab.url) || tab.id === undefined) {
    show(message("Open the site that you want to share.", "Then click the Fettle icon again."), footer(c));
    return;
  }
  show(el("p", { className: "soft" }, "Fettle reads your bots."));
  try {
    const t = await targets(c, tab.url);
    if (!t.site) {
      show(message("Fettle cannot share a sign-in for this page."), footer(c));
      return;
    }
    shareView(c, tab, t);
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      await saveConnection(null);
      show(message("Connect Chrome again.", AGAIN, "error"));
      return;
    }
    show(message("Fettle did not answer.", (err as Error).message, "error"), footer(c));
  }
}

void start();
