import createClient from "openapi-fetch";
import type { paths, SchemaErrorModel } from "./schema";

/** api is the typed client for the Fettle HTTP API. Calls use the session cookie of the same origin. */
export const api = createClient<paths>({ baseUrl: "", credentials: "same-origin" });

/** ApiError carries the HTTP status and the problem detail of a failed call. */
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

function problemText(error: unknown, status: number): string {
  if (error && typeof error === "object") {
    const e = error as SchemaErrorModel;
    const parts = [e.detail, ...(e.errors ?? []).map((x) => x.message)].filter((s): s is string => !!s);
    if (parts.length > 0) return parts.join(" ");
    if (e.title) return e.title;
  }
  return `HTTP ${status}`;
}

/** must resolves to the body of a successful call and throws ApiError for any other result. */
export async function must<T>(p: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await p;
  if (!response.ok) throw new ApiError(response.status, problemText(error, response.status));
  return data as T;
}

export function isUnauthorized(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}

export function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * exchangeHashToken reads the local token from the URL hash, trades it for the session cookie
 * and removes it from the address bar. It returns false when the server refused the token.
 */
export async function exchangeHashToken(): Promise<boolean> {
  const hash = window.location.hash.replace(/^#/, "");
  const params = new URLSearchParams(hash);
  const token = params.get("token");
  if (!token) return true;
  params.delete("token");
  const rest = params.toString();
  const url = window.location.pathname + window.location.search + (rest ? `#${rest}` : "");
  window.history.replaceState(window.history.state, "", url);
  const { response } = await api.POST("/api/v1/auth/local", { body: { token } });
  return response.ok;
}
