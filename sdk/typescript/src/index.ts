import createClient, { type Client } from "openapi-fetch";
import type { paths } from "./schema";

export type { paths, components } from "./schema";

/** Options of a Fettle API client. */
export interface FettleOptions {
  /** The server URL, for example https://fettle.acme.test or http://127.0.0.1:7420. */
  baseUrl: string;
  /** A personal access token (server mode) or the local token (local mode). */
  token: string;
  /** A fetch function to use instead of the global one. */
  fetch?: typeof globalThis.fetch;
}

/** createFettleClient returns a typed client for the Fettle HTTP API under /api/v1. */
export function createFettleClient(opts: FettleOptions): Client<paths> {
  return createClient<paths>({
    baseUrl: opts.baseUrl.replace(/\/+$/, ""),
    headers: { Authorization: `Bearer ${opts.token}` },
    fetch: opts.fetch,
  });
}
