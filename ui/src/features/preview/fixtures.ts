// Synthetic acme fixtures for the design preview. The dev build serves them; the release build does not include them.
import type { Bot, ChatSummary, Message } from "@/features/chat/types";
import type { RunDetail, RunEvent } from "@/features/runs/types";
import type { WizardModel } from "@/features/setup/Wizard";

const now = Date.now();
const at = (minAgo: number) => new Date(now - minAgo * 60_000).toISOString();

export const bots: Bot[] = [
  {
    id: "b-chief",
    name: "Chief",
    job: "Routes work and reports back",
    is_primary: true,
    status: "working",
    zone: "personal",
  },
  {
    id: "b-inbox",
    name: "Inbox",
    job: "Triage email and draft replies",
    is_primary: false,
    status: "waiting",
    zone: "personal",
  },
  {
    id: "b-research",
    name: "Research",
    job: "Find sources and summarize them",
    is_primary: false,
    status: "idle",
    zone: "browsing",
  },
  {
    id: "b-writer",
    name: "Writer",
    job: "Draft notes, posts and reports",
    is_primary: false,
    status: "idle",
    zone: "personal",
  },
];

export const chats: ChatSummary[] = [
  { id: "c-chief", kind: "direct", name: "Chief", bot_ids: ["b-chief"], unread: 0 },
  { id: "c-inbox", kind: "direct", name: "Inbox", bot_ids: ["b-inbox"], unread: 2 },
  { id: "c-research", kind: "direct", name: "Research", bot_ids: ["b-research"], unread: 0 },
  { id: "c-writer", kind: "direct", name: "Writer", bot_ids: ["b-writer"], unread: 0 },
  { id: "c-launch", kind: "group", name: "Launch prep", bot_ids: ["b-research", "b-writer"], unread: 0 },
];

export const messages: Message[] = [
  {
    id: "m1",
    chat_id: "c-chief",
    author_kind: "system",
    author_id: "",
    body: "Chief joined the space acme.",
    created_at: at(60 * 26),
  },
  {
    id: "m2",
    chat_id: "c-chief",
    author_kind: "user",
    author_id: "u-owner",
    body: "Compare the three standing desks on docs.test and draft a short note for the team.",
    created_at: at(60 * 25),
  },
  {
    id: "m3",
    chat_id: "c-chief",
    author_kind: "bot",
    author_id: "b-chief",
    body: "Research compared the three pages. Each claim links to its source.\n\n| Desk | Price | Height range | Warranty |\n|---|---|---|---|\n| Oak Lift | $420 | 64–128 cm | 5 years |\n| Birch Duo | $515 | 60–125 cm | 7 years |\n| Ash Pro | $389 | 70–118 cm | 3 years |\n\n**Ash Pro** costs least. **Birch Duo** has the longest warranty. The draft note is in `shared/desk-note.md`.",
    created_at: at(60 * 25 - 4),
    run: {
      run_id: "r-desks",
      envelope_id: "e-desks",
      step: "fanout",
      label: "Compare standing desks",
      helpers: 3,
      duration_ms: 4 * 60_000 + 12_000,
      cost_usd: 0.38,
      tokens: 48210,
      state: "done",
      verified: true,
    },
    attachments: [{ id: "a1", name: "desk-note.md", size: 2140, type: "text/markdown" }],
  },
  {
    id: "m4",
    chat_id: "c-chief",
    author_kind: "user",
    author_id: "u-owner",
    body: "Good. Send the note to team@acme.test.",
    created_at: at(14),
  },
  {
    id: "m5",
    chat_id: "c-chief",
    author_kind: "bot",
    author_id: "b-chief",
    body: "The email is ready. I need your approval before I send it.",
    created_at: at(13),
    card: {
      id: "card-1",
      kind: "approval",
      status: "pending",
      title: "Send an email",
      tool: "send_email",
      preview: {
        to: "team@acme.test",
        subject: "Standing desk options",
        body: "Hi all, here are the three desks we looked at…",
      },
    },
    run: {
      run_id: "r-send",
      envelope_id: "e-send",
      step: "single",
      label: "Send the desk note",
      helpers: 0,
      duration_ms: 41_000,
      cost_usd: 0.02,
      tokens: 3120,
      state: "waiting",
      verified: null,
    },
  },
  {
    id: "m6",
    chat_id: "c-chief",
    author_kind: "user",
    author_id: "u-owner",
    body: "While that waits: what is on my calendar this afternoon?",
    created_at: at(2),
  },
];

export const streamingReply =
  "You have two items this afternoon:\n\n- **14:00** Design review with the product team (45 min)\n- **16:30** Call with the office landlord about the lease\n\nI can prepare notes for the design review. Say the word.";

const types: [string, string, Partial<RunEvent>][] = [
  ["run_started", "Envelope from the owner's message", {}],
  ["context_built", "System prompt, 2 skills, memory 1.2 KB, 6 messages", { tokens: 3420 }],
  [
    "route_decision",
    "fanout · 3 parts · confidence 0.86",
    { detail: '{\n  "step": "fanout",\n  "parts": 3,\n  "rules_changed": []\n}', cost_usd: 0.001 },
  ],
  ["subagents_started", "3 helpers with briefs of 610, 588 and 602 tokens", {}],
];

function buildEvents(): RunEvent[] {
  const evs: RunEvent[] = [];
  let seq = 1;
  let ms = now - 60 * 25 * 60_000;
  const push = (type: string, summary: string, extra: Partial<RunEvent> = {}) => {
    ms += 900 + ((seq * 337) % 2400);
    evs.push({ seq: seq++, at: new Date(ms).toISOString(), type, summary, ...extra });
  };
  for (const [ty, s, x] of types) push(ty, s, x);
  for (const lane of ["helper 1", "helper 2", "helper 3"]) {
    const page = { "helper 1": "oak-lift", "helper 2": "birch-duo", "helper 3": "ash-pro" }[lane];
    push("model_turn", "Plan: open the product page and read the specification table", {
      lane,
      tokens: 1840,
      cost_usd: 0.004,
      duration_ms: 2100,
    });
    push("tool_call", `browser_navigate https://docs.test/${page}`, { lane, duration_ms: 640 });
    push("tool_call", "browser_snapshot · 3,812 characters, 14 elements", { lane, duration_ms: 210 });
    push("model_turn", "Extract price, height range and warranty", {
      lane,
      tokens: 4210,
      cost_usd: 0.011,
      duration_ms: 3900,
    });
    push("subagent_result", "3 findings, each with a source", {
      lane,
      detail: `summary: ${page} specification\nfindings:\n  - price (docs.test/${page}#price)\n  - height (docs.test/${page}#size)\n  - warranty (docs.test/${page}#terms)`,
    });
  }
  push("model_turn", "Synthesize the table and draft the note", { tokens: 9120, cost_usd: 0.071, duration_ms: 8200 });
  push("tool_intent", "write_file shared/desk-note.md", { detail: "idempotency_key: r-desks:14" });
  push("tool_result", "Wrote 2,140 bytes", { duration_ms: 40 });
  push("verify_started", "4 checks: llm, citations ×3", {});
  push("model_turn", "Verifier fetched 3 sources and matched 9 of 9 claims", {
    lane: "verifier",
    tokens: 6020,
    cost_usd: 0.019,
    duration_ms: 5400,
  });
  push("verify_result", "Pass · citations 100 %", {});
  push("run_finished", "Done · verified", {});
  return evs;
}

export const run: RunDetail = {
  id: "r-desks",
  label: "Compare standing desks",
  bot_name: "Chief",
  step: "fanout",
  state: "done",
  started_at: at(60 * 25),
  duration_ms: 4 * 60_000 + 12_000,
  cost_usd: 0.38,
  tokens_in: 41020,
  tokens_out: 7190,
  helpers: 3,
  verified: true,
  score: 100,
  events: buildEvents(),
  timing: { queue_ms: 140, router_ms: 1200, model_ms: 96_000, tools_ms: 154_000, first_token_ms: 900 },
};

export const wizard: WizardModel = {
  provider: {
    kind: "applecontainer",
    label: "Apple container",
    steps: [
      { label: "macOS 26 on Apple silicon", state: "pass" },
      { label: "Install the container runtime", state: "pass", detail: "1.2.0" },
      { label: "Download the computer image", state: "running", detail: "612 of 940 MB" },
      { label: "Start a test computer", state: "idle" },
    ],
  },
  modelChecks: [
    { key: "plain", state: "pass", detail: "412 ms" },
    { key: "tools", state: "pass", detail: "980 ms" },
    { key: "schema", state: "running" },
    { key: "vision", state: "idle" },
  ],
  timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
};
