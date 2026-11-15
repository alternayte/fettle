export type BotStatus = "idle" | "working" | "waiting" | "archived";

export interface Bot {
  id: string;
  name: string;
  job: string;
  is_primary: boolean;
  status: BotStatus;
  zone: string;
}

export interface ChatSummary {
  id: string;
  kind: "direct" | "group";
  name: string;
  bot_ids: string[];
  unread: number;
}

export type CardKind =
  | "approval"
  | "question"
  | "budget"
  | "uncertain"
  | "takeover"
  | "turn_limit"
  | "change"
  | "warning"
  | "verification"
  | "check"
  | "proposal";
export type CardStatus = "pending" | "allowed" | "denied" | "answered" | "expired";

export interface Card {
  id: string;
  kind: CardKind;
  status: CardStatus;
  title: string;
  /** Tool name for approval cards. */
  tool?: string;
  /** Short preview of the tool arguments, already redacted by the server. */
  preview?: Record<string, string>;
  /** Options for question cards. */
  options?: string[];
  /** Free text detail. */
  detail?: string;
  hard_ask?: boolean;
  answer?: string;
  /** Kind-specific fields; a takeover card holds bot_id. */
  data?: Record<string, string>;
}

export type RunState = "queued" | "running" | "waiting" | "verifying" | "done" | "failed" | "escalated" | "cancelled";

export interface RunLine {
  run_id: string;
  envelope_id: string;
  step: "direct" | "single" | "fanout" | "forked" | "handoff" | "hire";
  label: string;
  helpers: number;
  duration_ms: number;
  cost_usd: number;
  tokens: number;
  state: RunState;
  verified: boolean | null;
}

export interface Attachment {
  id: string;
  name: string;
  size: number;
  type: string;
}

export interface Message {
  id: string;
  chat_id: string;
  author_kind: "user" | "bot" | "system";
  author_id: string;
  body: string;
  created_at: string;
  streaming?: boolean;
  card?: Card;
  run?: RunLine;
  attachments?: Attachment[];
  /** The message this one replies to. */
  reply_to?: string;
  /** Reactions by person ID. */
  reactions?: Record<string, string>;
}
