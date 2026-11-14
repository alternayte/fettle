/** Calls to the sign-in routes of server mode (auth-all under /api/auth). */

export type SignInResult = "ok" | "wrong" | "limited" | "failed";

export async function signIn(email: string, password: string): Promise<SignInResult> {
  const r = await fetch("/api/auth/sign-in/email", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (r.ok) return "ok";
  if (r.status === 429) return "limited";
  if (r.status === 400 || r.status === 401 || r.status === 403) return "wrong";
  return "failed";
}

export async function signOut(): Promise<void> {
  await fetch("/api/auth/sign-out", { method: "POST", credentials: "same-origin" });
}

export type AccessToken = {
  id: string;
  name: string;
  start: string;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
};

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(`/api/auth${path}`, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`${r.status}`);
  return (await r.json()) as T;
}

export const listTokens = async () => {
  const r = await call<{ keys?: AccessToken[] } | AccessToken[]>("GET", "/api-keys");
  return Array.isArray(r) ? r : (r.keys ?? []);
};
export const createToken = (name: string) =>
  call<{ key: AccessToken; plaintext: string }>("POST", "/api-keys", { name });
export const revokeToken = (id: string) => call<unknown>("POST", `/api-keys/${id}/revoke`, {});
