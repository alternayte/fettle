import type { RunState } from "@/features/chat/types";

export interface RunEvent {
  seq: number;
  at: string;
  type: string;
  /** One line, already redacted by the server. */
  summary: string;
  /** Optional detail text, at most a preview. */
  detail?: string;
  cost_usd?: number;
  tokens?: number;
  duration_ms?: number;
  /** Sub-agent or verifier label when the event belongs to a child run. */
  lane?: string;
}

export interface RunDetail {
  id: string;
  label: string;
  bot_name: string;
  step: string;
  state: RunState;
  started_at: string;
  duration_ms: number;
  cost_usd: number;
  tokens_in: number;
  tokens_out: number;
  helpers: number;
  verified: boolean | null;
  score?: number;
  events: RunEvent[];
  /** Where the time of the run went, from the journal. */
  timing: RunTiming;
}

export interface RunTiming {
  /** Wait from the creation of the run to its first worker. */
  queue_ms: number;
  router_ms: number;
  /** Time of all model turns. */
  model_ms: number;
  /** Time of all tool calls. */
  tools_ms: number;
  /** Time to the first streamed text of the first model turn, 0 when unknown. */
  first_token_ms: number;
}
