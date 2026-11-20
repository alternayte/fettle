// The connection to one Fettle server: its address, a token that can only share a sign-in,
// and the account. chrome.storage.local keeps it on this device only.

export type Connection = { server: string; token: string; account: string };

export type Bot = { id: string; name: string; requests: string[] };
export type Space = { id: string; name: string; people: number; bots: Bot[] };
export type Targets = { account: string; site?: string; spaces: Space[] };

export type SessionCookie = {
  name: string;
  value: string;
  domain: string;
  path?: string;
  host_only?: boolean;
  secure?: boolean;
  http_only?: boolean;
  same_site?: string;
  expires?: number;
};

export type Shared = { site: string; cookies: number; storage: number; continued: boolean };

/** ApiError carries the HTTP status and the message of the server. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function loadConnection(): Promise<Connection | null> {
  const v = await chrome.storage.local.get("connection");
  const c = v.connection as Connection | undefined;
  return c && c.server && c.token ? c : null;
}

export async function saveConnection(c: Connection | null): Promise<void> {
  if (c) await chrome.storage.local.set({ connection: c });
  else await chrome.storage.local.remove("connection");
}

/** normalizeServer accepts "127.0.0.1:7420" or "https://fettle.acme.test/" and returns an origin. */
export function normalizeServer(input: string): string {
  let s = input.trim();
  if (!/^https?:\/\//i.test(s)) s = (/^(localhost|127\.|\[::1\])/i.test(s) ? "http://" : "https://") + s;
  return new URL(s).origin;
}

async function call<T>(server: string, path: string, init: { method?: string; token?: string; body?: unknown }): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (init.token) headers.Authorization = `Bearer ${init.token}`;
  let resp: Response;
  try {
    resp = await fetch(server + "/api/v1" + path, {
      method: init.method ?? "GET",
      headers,
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
      credentials: "omit",
    });
  } catch {
    throw new ApiError(0, `Chrome cannot reach ${server}. Make sure that Fettle runs.`);
  }
  const text = await resp.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    // The body is not JSON; the status says enough.
  }
  if (!resp.ok) {
    const d = data as { detail?: string; errors?: { message?: string }[] } | undefined;
    const msg = [d?.detail, ...(d?.errors ?? []).map((e) => e.message)].filter(Boolean).join(" ");
    throw new ApiError(resp.status, msg || `HTTP ${resp.status}`);
  }
  return data as T;
}

export async function claim(server: string, code: string): Promise<Connection> {
  const r = await call<{ token: string; server: string; account: string }>(server, "/extension/claim", {
    method: "POST",
    body: { code },
  });
  return { server, token: r.token, account: r.account };
}

export function targets(c: Connection, url?: string): Promise<Targets> {
  const q = url ? `?url=${encodeURIComponent(url)}` : "";
  return call<Targets>(c.server, "/extension/targets" + q, { token: c.token });
}

export function share(
  c: Connection,
  space: string,
  bot: string,
  body: { url: string; cookies: SessionCookie[]; storage?: Record<string, string> },
): Promise<Shared> {
  return call<Shared>(c.server, `/spaces/${space}/bots/${bot}/sign-ins`, { method: "POST", token: c.token, body });
}

/** pairingFromURL reads the server and the code from the pairing page of a Fettle server. */
export function pairingFromURL(url: string | undefined): { server: string; code: string } | null {
  if (!url) return null;
  try {
    const u = new URL(url);
    const m = /code=([A-Z0-9-]+)/.exec(u.hash);
    if (u.pathname !== "/pair" || !m) return null;
    return { server: u.origin, code: m[1] };
  } catch {
    return null;
  }
}
