import en from "@/copy/en.json";

type Tree = { [k: string]: string | Tree };

function lookup(path: string): string {
  let node: string | Tree = en as Tree;
  for (const part of path.split(".")) {
    if (typeof node === "string" || !(part in node)) {
      throw new Error(`missing copy key: ${path}`);
    }
    node = node[part];
  }
  if (typeof node !== "string") throw new Error(`copy key is not a string: ${path}`);
  return node;
}

/** has reports whether a copy key exists, for keys built from server data. */
export function has(path: string): boolean {
  try {
    lookup(path);
    return true;
  } catch {
    return false;
  }
}

/** t returns the UI string at path with {name} placeholders filled from vars. */
export function t(path: string, vars?: Record<string, string | number>): string {
  const s = lookup(path);
  if (!vars) return s;
  return s.replace(/\{(\w+)\}/g, (_, k: string) => (k in vars ? String(vars[k]) : `{${k}}`));
}

/** tn picks path.one or path.other by n and fills {n}. */
export function tn(path: string, n: number, vars?: Record<string, string | number>): string {
  return t(`${path}.${n === 1 ? "one" : "other"}`, { n, ...vars });
}
