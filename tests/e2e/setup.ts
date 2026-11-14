import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export const STATE = join(tmpdir(), "fettle-e2e-state.json");
const PORT = 7480;

function waitLine(p: ChildProcess, re: RegExp, ms: number): Promise<string> {
  return new Promise((res, rej) => {
    const timer = setTimeout(() => rej(new Error(`timeout waiting for ${re}`)), ms);
    const on = (b: Buffer) => {
      const m = b.toString().match(re);
      if (m) {
        clearTimeout(timer);
        res(m[1] ?? m[0]);
      }
    };
    p.stdout?.on("data", on);
    p.stderr?.on("data", on);
  });
}

const IMAGE = process.env.FETTLE_COMPUTER_IMAGE ?? "fettle-computer:dev";

/** fixtureSites runs one fixture web container per site and returns FETTLE_COMPUTER_HOSTS. */
function fixtureSites(): { hosts: string; containers: string[] } {
  const bin = resolve("../../bin/linux/fettle-fakes");
  if (!existsSync(bin)) throw new Error("build bin/linux/fettle-fakes first: just e2e");
  const hosts: string[] = [];
  const containers: string[] = [];
  for (const site of ["docs.test", "shop.test", "evil.test", "slow.test"]) {
    const id = execFileSync("docker", ["run", "-d", "--rm", "--user", "0", "--entrypoint", "/fakes/fettle-fakes", "-v", `${resolve("../../bin/linux")}:/fakes:ro`, IMAGE, "fake", "serve", "--web-addr", ":80"]).toString().trim();
    containers.push(id);
    const ip = execFileSync("docker", ["inspect", "-f", "{{.NetworkSettings.IPAddress}}", id]).toString().trim();
    hosts.push(`${site}=${ip}`);
  }
  return { hosts: hosts.join(","), containers };
}

export default async function setup() {
  const bin = resolve(process.env.FETTLE_BIN ?? "../../bin/fettle");
  const fakesBin = resolve(process.env.FETTLE_FAKES_BIN ?? "../../bin/fettle-fakes");
  if (!existsSync(bin) || !existsSync(fakesBin)) throw new Error("build bin/fettle and bin/fettle-fakes first: just e2e");
  const fake = spawn(fakesBin, ["fake", "serve", "--model-addr", "127.0.0.1:0", "--scripts", resolve("scripts")], { stdio: ["ignore", "pipe", "pipe"] });
  const fakeURL = await waitLine(fake, /FAKE_MODEL_URL=(\S+)/, 20_000);
  const data = mkdtempSync(join(tmpdir(), "fettle-e2e-"));
  const sites = fixtureSites();
  const server = spawn(bin, ["serve", "--data", data, "--addr", `127.0.0.1:${PORT}`], {
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      FETTLE_NO_KEYCHAIN: "1",
      FETTLE_LOG_LEVEL: "info",
      FETTLE_DEFAULT_PROVIDER: "docker",
      FETTLE_COMPUTER_IMAGE: IMAGE,
      FETTLE_COMPUTER_HOSTS: sites.hosts,
    },
  });
  await waitLine(server, /fettle is ready/, 60_000);
  const token = readFileSync(join(data, "local.token"), "utf8").trim();
  const api = async (method: string, path: string, body?: unknown) => {
    const r = await fetch(`http://127.0.0.1:${PORT}/api/v1${path}`, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${await r.text()}`);
    return r.status === 204 ? null : r.json();
  };
  const me = await api("GET", "/me");
  const space = me.spaces[0].id;
  await api("PATCH", "/me", { name: "Ada" });
  const p = await api("POST", `/spaces/${space}/model-providers`, { name: "Fake", api_type: "openai-chat", base_url: fakeURL, key: "sk-fake-e2e-key-0001" });
  await api("POST", `/spaces/${space}/model-providers/${p.id}/test`, { model: "fake/large" });
  for (const role of ["owner", "summarizer"]) {
    await api("PUT", `/spaces/${space}/model-roles/${role}`, { targets: [{ provider_id: p.id, model: "fake/large", price_in: 3, price_out: 15, context_window: 128000 }] });
  }
  writeFileSync(STATE, JSON.stringify({ url: `http://127.0.0.1:${PORT}`, token, space, data, fakeURL, pids: [server.pid, fake.pid], containers: sites.containers }));
  server.unref();
  fake.unref();
}
