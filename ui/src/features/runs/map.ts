import type { SchemaEventView, SchemaRunView } from "@/api/schema";
import type { RunState } from "@/features/chat/types";
import { has, t } from "@/lib/copy";
import { formatUSD } from "@/lib/format";
import type { RunDetail, RunEvent, RunTiming } from "./types";

type Data = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");
const num = (v: unknown) => (typeof v === "number" ? v : undefined);

function firstLine(s: string, max = 140): string {
  const line = s.trim().split("\n", 1)[0] ?? "";
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

function toolSummary(d: Data): string {
  const tool = str(d.tool);
  const err = str(d.error);
  const res = str(d.result);
  const tail = err ? t("timeline.summary.error", { error: firstLine(err, 100) }) : firstLine(res, 100);
  return tail ? `${tool}: ${tail}` : tool;
}

function summarize(type: string, d: Data): string {
  switch (type) {
    case "run_started":
      return firstLine(str(d.goal));
    case "model_turn": {
      const text = firstLine(str(d.text));
      if (text) return text;
      const calls = Array.isArray(d.tool_calls) ? (d.tool_calls as Data[]).map((c) => str(c.name)).filter(Boolean) : [];
      return calls.length > 0 ? t("timeline.summary.calls", { tools: calls.join(", ") }) : str(d.model);
    }
    case "model_switch":
      return t("timeline.summary.switch", { from: str(d.from), to: str(d.to), reason: firstLine(str(d.reason), 80) });
    case "tool_call":
    case "tool_result":
      return toolSummary(d);
    case "tool_intent":
    case "tool_uncertain":
      return str(d.tool);
    case "steer":
      return firstLine(str(d.text));
    case "card_posted":
      return has(`card.kind.${str(d.kind)}`) ? t(`card.kind.${str(d.kind)}`) : str(d.kind);
    case "card_answered":
      return [str(d.action), str(d.value)].filter(Boolean).join(": ");
    case "summary":
      return firstLine(str(d.text));
    case "budget_reached":
      return t("timeline.summary.budget", {
        estimate: formatUSD(num(d.estimate_usd) ?? 0),
        left: formatUSD(num(d.left_usd) ?? 0),
      });
    case "cancel":
      return t("timeline.summary.cancel");
    case "run_finished": {
      const status = str(d.status);
      const label = has(`run.state.${status}`) ? t(`run.state.${status}`) : status;
      const more = firstLine(str(d.error) || str(d.summary), 100);
      return more ? `${label}: ${more}` : label;
    }
    default:
      for (const k of ["text", "summary", "goal", "reason", "tool", "kind"]) {
        const v = str(d[k]);
        if (v) return firstLine(v);
      }
      return "";
  }
}

export function toRunEvent(ev: SchemaEventView): RunEvent {
  const d = (ev.data && typeof ev.data === "object" ? ev.data : {}) as Data;
  const usage = (d.usage ?? {}) as Data;
  const tokens = ev.type === "model_turn" ? (num(usage.in) ?? 0) + (num(usage.out) ?? 0) : undefined;
  let detail = ev.data === undefined || ev.data === null ? undefined : JSON.stringify(ev.data, null, 2);
  if (detail && ev.truncated) detail = `${t("timeline.truncated")}\n${detail}`;
  return {
    seq: ev.seq,
    at: ev.at,
    type: ev.type,
    summary: summarize(ev.type, d),
    detail: detail && detail !== "{}" ? detail : undefined,
    cost_usd: ev.type === "model_turn" ? num(d.cost_usd) : undefined,
    tokens: tokens || undefined,
    duration_ms: num(d.duration_ms),
  };
}

/** toTiming adds the timings of the journal: queue and router from run_started, model and
 * tool times from their events, and the first token of the first model turn. */
export function toTiming(events: SchemaEventView[]): RunTiming {
  const out: RunTiming = { queue_ms: 0, router_ms: 0, model_ms: 0, tools_ms: 0, first_token_ms: 0 };
  const turn = (d: Data) => {
    out.model_ms += num(d.duration_ms) ?? 0;
    if (!out.first_token_ms) out.first_token_ms = num(d.first_token_ms) ?? 0;
  };
  for (const ev of events) {
    const d = (ev.data && typeof ev.data === "object" ? ev.data : {}) as Data;
    switch (ev.type) {
      case "run_started":
        out.queue_ms = num(d.queue_ms) ?? 0;
        out.router_ms = num(d.router_ms) ?? 0;
        break;
      case "model_turn":
        turn(d);
        break;
      case "run_finished":
        if (d.turn && typeof d.turn === "object") turn(d.turn as Data);
        break;
      case "tool_call":
      case "tool_result":
        out.tools_ms += num(d.duration_ms) ?? 0;
        break;
    }
  }
  return out;
}

export function toRunDetail(r: SchemaRunView): RunDetail {
  return {
    id: r.line.run_id,
    label: r.line.label || firstLine(r.goal, 60),
    bot_name: r.bot_name,
    step: r.line.step,
    state: r.line.state as RunState,
    started_at: r.started_at ?? "",
    duration_ms: r.line.duration_ms,
    cost_usd: r.line.cost_usd,
    tokens_in: r.tokens_in,
    tokens_out: r.tokens_out,
    helpers: r.line.helpers,
    verified: r.line.verified ?? null,
    events: (r.events ?? []).map(toRunEvent),
    timing: toTiming(r.events ?? []),
  };
}
