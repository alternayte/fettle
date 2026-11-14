import { test as base, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { STATE } from "./setup";

export type State = { url: string; token: string; space: string; data: string; fakeURL: string; pushURL: string; mcpURL: string };

export const state = (): State => JSON.parse(readFileSync(STATE, "utf8"));

export async function api(method: string, path: string, body?: unknown) {
  const s = state();
  const r = await fetch(`${s.url}/api/v1${path}`, {
    method,
    headers: { Authorization: `Bearer ${s.token}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${await r.text()}`);
  return r.status === 204 ? null : r.json();
}

/** signIn opens the app with the local token in the URL hash, as fettle up prints it. */
export async function signIn(page: Page, path = "/") {
  await page.goto(`${path}#token=${state().token}`);
}

export const test = base;
export { expect };
