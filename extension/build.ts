// Builds the extension into dist/: the bundled scripts and the static files.
import { cpSync, rmSync } from "node:fs";

rmSync("dist", { recursive: true, force: true });
const out = await Bun.build({
  entrypoints: ["src/popup.ts", "src/background.ts"],
  outdir: "dist",
  target: "browser",
  format: "esm",
  minify: false,
});
if (!out.success) {
  for (const m of out.logs) console.error(m);
  process.exit(1);
}
cpSync("public", "dist", { recursive: true });
console.log("extension: built into extension/dist");
