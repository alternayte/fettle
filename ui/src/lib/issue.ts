/** The note at the end of a body that newIssueURL cut. The server uses the same text. */
export const cutNote = "\n\n[The text is cut. Paste the full text from Fettle.]";

/**
 * newIssueURL returns the GitHub new-issue URL of repo with the title and the body. When
 * the URL would be longer than max, it cuts the body and adds cutNote.
 */
export function newIssueURL(repo: string, title: string, body: string, max: number): { url: string; cut: boolean } {
  const build = (b: string) =>
    `https://github.com/${repo}/issues/new?` + new URLSearchParams({ title, body: b }).toString();
  const full = build(body);
  if (full.length <= max) return { url: full, cut: false };
  const chars = Array.from(body);
  let lo = 0;
  let hi = chars.length;
  while (lo < hi) {
    const mid = Math.floor((lo + hi + 1) / 2);
    if (build(chars.slice(0, mid).join("") + cutNote).length <= max) lo = mid;
    else hi = mid - 1;
  }
  return { url: build(chars.slice(0, lo).join("") + cutNote), cut: true };
}

/** issueBody joins the parts of a report and the diagnostics block, as the server does. */
export function issueBody(parts: { what: string; expected: string; steps: string }, diagnostics: string): string {
  const out: string[] = [];
  const add = (h: string, v: string) => {
    if (v.trim()) out.push(`### ${h}\n\n${v.trim()}\n\n`);
  };
  add("What happened", parts.what);
  add("What I expected", parts.expected);
  add("Steps", parts.steps);
  return out.join("") + diagnostics;
}
