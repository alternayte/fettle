import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export const OWNER = { email: "owner@acme.test", name: "Owner", password: "acme-owner-pass-4410" };
export const MEMBER = { email: "member@acme.test", name: "Member", password: "acme-member-pass-5521" };

/** A second Fettle server in server mode, with one owner account, for tests that need two people. */
export type ServerMode = { url: string; stop: () => Promise<void> };

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

function ready(p: ChildProcess, ms: number): Promise<void> {
  return new Promise((res, rej) => {
    let log = "";
    const timer = setTimeout(() => rej(new Error(`server mode did not start:\n${log}`)), ms);
    const on = (b: Buffer) => {
      log += b.toString();
      if (log.includes("fettle is ready")) {
        clearTimeout(timer);
        res();
      }
    };
    p.stdout?.on("data", on);
    p.stderr?.on("data", on);
    p.once("exit", (code) => {
      clearTimeout(timer);
      rej(new Error(`server mode ended with ${code}:\n${log}`));
    });
  });
}

/** startServerMode makes the owner with fettle admin create-user, then runs fettle serve --mode server. */
export async function startServerMode(): Promise<ServerMode> {
  const bin = resolve(process.env.FETTLE_BIN ?? "../../bin/fettle");
  const data = mkdtempSync(join(tmpdir(), "fettle-e2e-server-"));
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const env = {
    ...process.env,
    FETTLE_NO_KEYCHAIN: "1",
    FETTLE_LOG_LEVEL: "info",
    FETTLE_MODE: "server",
    FETTLE_BASE_URL: url,
    FETTLE_MASTER_KEY: randomBytes(32).toString("base64"),
    FETTLE_PASSWORD: OWNER.password,
    FETTLE_DEFAULT_PROVIDER: "docker",
  };
  execFileSync(bin, ["admin", "create-user", "--data", data, "--email", OWNER.email, "--name", OWNER.name, "--admin"], {
    env,
    stdio: "pipe",
  });
  const server = spawn(bin, ["serve", "--mode", "server", "--data", data, "--addr", `127.0.0.1:${port}`], {
    env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  await ready(server, 60_000);
  return {
    url,
    stop: async () => {
      const ended = new Promise((res) => server.once("exit", res));
      server.kill("SIGTERM");
      await Promise.race([ended, new Promise((res) => setTimeout(res, 10_000))]);
      rmSync(data, { recursive: true, force: true });
    },
  };
}
