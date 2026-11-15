import type { SchemaBotView, SchemaChatView, SchemaMessageView, SchemaRunLine } from "@/api/schema";
import type { Bot, Card, ChatSummary, Message, RunLine } from "./types";

export function toBot(b: SchemaBotView): Bot {
  return {
    id: b.id,
    name: b.name,
    job: b.job,
    is_primary: b.is_primary,
    status: b.status === "archived" ? "archived" : b.activity,
    zone: b.zone_name,
    screen: b.zone_size === "desktop",
  };
}

export function toChat(c: SchemaChatView): ChatSummary {
  // The API has no unread count yet.
  return { id: c.id, kind: c.kind, name: c.name, bot_ids: c.bot_ids ?? [], unread: 0 };
}

export function toRunLine(r: SchemaRunLine): RunLine {
  return {
    run_id: r.run_id,
    envelope_id: r.envelope_id,
    step: r.step as RunLine["step"],
    label: r.label,
    helpers: r.helpers,
    duration_ms: r.duration_ms,
    cost_usd: r.cost_usd,
    tokens: r.tokens,
    state: r.state as RunLine["state"],
    verified: r.verified ?? null,
  };
}

function toCard(c: NonNullable<SchemaMessageView["card"]>): Card {
  return {
    id: c.id,
    kind: c.kind as Card["kind"],
    status: c.status as Card["status"],
    title: c.title,
    tool: c.tool || undefined,
    preview: c.preview && Object.keys(c.preview).length > 0 ? c.preview : undefined,
    options: c.options ?? undefined,
    detail: c.detail || undefined,
    hard_ask: c.hard_ask,
    answer: c.answer || undefined,
    data: c.data ?? undefined,
  };
}

export function toMessage(m: SchemaMessageView, run?: SchemaRunLine): Message {
  return {
    id: m.id,
    chat_id: m.chat_id,
    author_kind: m.author_kind as Message["author_kind"],
    author_id: m.author_id,
    body: m.body,
    created_at: m.created_at,
    streaming: m.streaming,
    card: m.card ? toCard(m.card) : undefined,
    run: run ? toRunLine(run) : undefined,
    attachments: m.attachments ?? undefined,
    reply_to: m.reply_to || undefined,
    reactions: m.reactions && Object.keys(m.reactions).length > 0 ? m.reactions : undefined,
  };
}
