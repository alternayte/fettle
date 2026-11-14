// Copy lint for ui/src/copy/en.json (REQ-UX-009): sentences of 20 words or fewer and no banned words.

export const MAX_WORDS = 20;

// Domain words to avoid in UI copy (SDD §1.2) and words that STE style does not use.
export const BANNED = [
  "agent",
  "agents",
  "sandbox",
  "sandboxes",
  "vm",
  "vms",
  "task",
  "tasks",
  "simply",
  "just",
  "easy",
  "easily",
  "seamless",
  "seamlessly",
  "powerful",
  "awesome",
  "amazing",
  "please",
  "oops",
  "whoops",
  "magic",
  "magical",
  "utilize",
  "leverage",
  "very",
  "really",
];

export type Problem = { key: string; text: string; reason: string };

function sentences(s: string): string[] {
  return s
    .replace(/\{[a-zA-Z0-9_]+\}/g, "x")
    .split(/(?<=[.!?:])\s+|\n+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

function words(s: string): string[] {
  return s.split(/\s+/).filter((w) => /[\p{L}\p{N}]/u.test(w));
}

export function lintString(key: string, text: string): Problem[] {
  const out: Problem[] = [];
  for (const s of sentences(text)) {
    const n = words(s).length;
    if (n > MAX_WORDS) out.push({ key, text, reason: `sentence has ${n} words (limit ${MAX_WORDS})` });
  }
  const lower = text.toLowerCase();
  for (const b of BANNED) {
    if (new RegExp(`(^|[^\\p{L}])${b}([^\\p{L}]|$)`, "u").test(lower)) {
      out.push({ key, text, reason: `uses the banned word "${b}"` });
    }
  }
  return out;
}

type Tree = { [k: string]: string | Tree };

export function lintTree(tree: Tree, prefix = ""): Problem[] {
  const out: Problem[] = [];
  for (const [k, v] of Object.entries(tree)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (typeof v === "string") out.push(...lintString(key, v));
    else out.push(...lintTree(v, key));
  }
  return out;
}

if (import.meta.main) {
  const file = process.argv[2] ?? "src/copy/en.json";
  const tree = (await Bun.file(file).json()) as Tree;
  const problems = lintTree(tree);
  for (const p of problems) console.error(`copy-lint: ${p.key}: ${p.reason}: ${JSON.stringify(p.text)}`);
  if (problems.length > 0) process.exit(1);
  console.log(`copy-lint: ${file} passes`);
}
