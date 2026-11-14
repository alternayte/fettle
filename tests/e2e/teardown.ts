import { readFileSync, rmSync } from "node:fs";
import { STATE } from "./setup";

export default async function teardown() {
  const s = JSON.parse(readFileSync(STATE, "utf8"));
  for (const pid of s.pids) {
    try {
      process.kill(pid, "SIGTERM");
    } catch {
      // The process already ended.
    }
  }
  rmSync(STATE, { force: true });
}
