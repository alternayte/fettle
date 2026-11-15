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

export type Passkey = { id: string; name: string; createdAt: string; lastUsedAt: string | null };

/** PasskeyError carries the auth-all error code, such as SESSION_NOT_FRESH. */
export class PasskeyError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.code = code;
  }
}

async function auth<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(`/api/auth${path}`, {
    method,
    credentials: "same-origin",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await r.text();
  const data = text ? JSON.parse(text) : {};
  if (!r.ok) throw new PasskeyError(data?.error?.code ?? String(r.status), data?.error?.message ?? String(r.status));
  return data as T;
}

/** passkeysSupported reports whether this browser can make and use passkeys. */
export const passkeysSupported = () =>
  typeof window !== "undefined" &&
  "PublicKeyCredential" in window &&
  typeof PublicKeyCredential.parseCreationOptionsFromJSON === "function";

type Ceremony<O> = { ceremony: string; options: { publicKey: O } };

export const listPasskeys = async () => (await auth<{ passkeys: Passkey[] }>("GET", "/passkeys")).passkeys ?? [];
export const deletePasskey = (id: string) => auth<unknown>("DELETE", `/passkeys/${id}`);

/** addPasskey runs the registration ceremony of the signed-in person. */
export async function addPasskey(name: string): Promise<void> {
  const begin = await auth<Ceremony<PublicKeyCredentialCreationOptionsJSON>>("POST", "/passkeys/register/begin", {});
  const created = (await navigator.credentials.create({
    publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(begin.options.publicKey),
  })) as PublicKeyCredential | null;
  if (!created) throw new PasskeyError("CANCELLED", "cancelled");
  await auth("POST", "/passkeys/register/finish", { ceremony: begin.ceremony, name, credential: created.toJSON() });
}

/** passkeySignIn runs the discoverable sign-in ceremony. "mfa" means the passkey did not verify the person. */
export async function passkeySignIn(): Promise<"ok" | "mfa" | "cancelled" | "failed"> {
  try {
    const begin = await auth<Ceremony<PublicKeyCredentialRequestOptionsJSON>>("POST", "/passkeys/sign-in/begin", {});
    const assertion = (await navigator.credentials.get({
      publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(begin.options.publicKey),
    })) as PublicKeyCredential | null;
    if (!assertion) return "cancelled";
    const out = await auth<{ mfaRequired?: boolean }>("POST", "/passkeys/sign-in/finish", {
      ceremony: begin.ceremony,
      credential: assertion.toJSON(),
    });
    return out.mfaRequired ? "mfa" : "ok";
  } catch (err) {
    if (err instanceof DOMException && err.name === "NotAllowedError") return "cancelled";
    return "failed";
  }
}

export type SSOMethod = { kind: "oidc" | "saml"; name: string; url: string };

/** listSSO returns the single sign-on methods of this server; none in the community edition. */
export async function listSSO(): Promise<SSOMethod[]> {
  const r = await fetch("/api/v1/sso", { credentials: "same-origin" });
  if (!r.ok) return [];
  return ((await r.json()) as { methods?: SSOMethod[] }).methods ?? [];
}
