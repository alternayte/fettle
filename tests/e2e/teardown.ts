import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { STATE } from "./setup";

export default async function teardown() {
  if (!existsSync(STATE)) return;
  const s = JSON.parse(readFileSync(STATE, "utf8"));
  for (const pid of s.pids) {
    try {
      process.kill(pid, "SIGTERM");
    } catch {
      // The process already ended.
    }
  }
  // The fixture sites and the computers that the server made.
  const computers = execFileSync("docker", ["ps", "-aq", "--filter", "label=fettle.computer=1"]).toString().split(/\s+/).filter(Boolean);
  for (const id of [...(s.containers ?? []), ...computers]) {
    try {
      execFileSync("docker", ["rm", "-f", id], { stdio: "ignore" });
    } catch {
      // The container already ended.
    }
  }
  rmSync(STATE, { force: true });
}
