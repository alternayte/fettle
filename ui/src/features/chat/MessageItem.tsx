import { memo } from "react";
import { motion, useReducedMotion } from "motion/react";
import { CornerUpLeft, Paperclip, SmilePlus } from "lucide-react";
import { t } from "@/lib/copy";
import { formatClock } from "@/lib/format";
import { cn } from "@/lib/utils";
import { BotMark } from "./BotMark";
import { CardView, type CardAnswer } from "./CardView";
import { Markdown } from "./Markdown";
import { RunLine } from "./RunLine";
import type { Bot, Message } from "./types";

function formatSize(n: number) {
  if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const QUICK = ["👍", "✅", "❤️", "👀"];

/** Reactions shows each emoji with its count; the viewer's own reaction is marked. */
function Reactions({
  reactions,
  me,
  onReact,
}: {
  reactions: Record<string, string>;
  me: string;
  onReact: (e: string) => void;
}) {
  const counts = new Map<string, number>();
  for (const e of Object.values(reactions)) counts.set(e, (counts.get(e) ?? 0) + 1);
  return (
    <ul className="mt-1 flex flex-wrap gap-1">
      {[...counts].map(([e, n]) => (
        <li key={e}>
          <button
            type="button"
            onClick={() => onReact(e)}
            aria-pressed={reactions[me] === e}
            aria-label={t("chat.reactWith", { emoji: e })}
            className={cn(
              "inline-flex h-6 items-center gap-1 rounded-xs border px-1.5 text-xs",
              reactions[me] === e ? "border-glaze/60 bg-glaze-wash text-ink" : "border-rule bg-surface text-ink-soft",
            )}
          >
            <span aria-hidden>{e}</span>
            <span className="figures">{n}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

export const MessageItem = memo(function MessageItem({
  message,
  bot,
  continued,
  activeRunId,
  onOpenRun,
  onAnswer,
  quoted,
  me,
  onReply,
  onReact,
  fileHref,
}: {
  message: Message;
  bot?: Bot;
  continued: boolean;
  activeRunId?: string;
  onOpenRun: (runId: string) => void;
  onAnswer: (cardId: string, a: CardAnswer) => void;
  /** The message this one replies to, with its author's name. */
  quoted?: { name: string; body: string };
  me?: string;
  onReply?: (m: Message) => void;
  onReact?: (id: string, emoji: string) => void;
  fileHref?: (attachmentId: string) => string;
}) {
  const reduce = useReducedMotion();
  const isUser = message.author_kind === "user";
  const isSystem = message.author_kind === "system";
  const name = isUser ? t("chat.you") : (bot?.name ?? t("chat.unknownBot"));

  if (isSystem) {
    return (
      <div className="grid grid-cols-[3.25rem_1fr] gap-x-3 py-1 text-xs text-ink-faint sm:grid-cols-[4rem_1fr]">
        <span className="figures pt-px text-right">{formatClock(message.created_at)}</span>
        <div className="min-w-0">
          {message.body && <span>{message.body}</span>}
          {message.card && <CardView card={message.card} onAnswer={(a) => onAnswer(message.card!.id, a)} />}
        </div>
      </div>
    );
  }

  return (
    <motion.article
      initial={reduce || !message.streaming ? false : { opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.16 }}
      aria-label={t("chat.messageFrom", { name })}
      className={cn(
        "group relative grid grid-cols-[3.25rem_1fr] gap-x-3 sm:grid-cols-[4rem_1fr]",
        continued ? "pt-1" : "pt-4",
      )}
    >
      <div className="flex justify-end pt-0.5">
        {continued ? (
          <time
            dateTime={message.created_at}
            className="figures pt-0.5 text-ink-faint opacity-0 transition-opacity duration-[var(--dur-1)] group-hover:opacity-100"
          >
            {formatClock(message.created_at)}
          </time>
        ) : isUser ? (
          <span
            aria-hidden
            className="inline-flex size-7 items-center justify-center rounded-xs bg-ink text-2xs font-[var(--weight-strong)] text-surface"
          >
            {t("chat.youMark")}
          </span>
        ) : (
          <BotMark name={name} />
        )}
      </div>
      <div className="min-w-0">
        {!continued && (
          <div className="mb-0.5 flex items-baseline gap-2">
            <span className="text-sm font-[var(--weight-strong)] text-ink">{name}</span>
            {bot?.is_primary && <span className="label-caps">{t("bots.primary")}</span>}
            <time dateTime={message.created_at} className="figures text-ink-faint">
              {formatClock(message.created_at)}
            </time>
          </div>
        )}
        {quoted && (
          <p
            aria-label={t("chat.quoted", { name: quoted.name })}
            className="mb-1 flex max-w-[var(--measure)] items-center gap-1.5 truncate border-l border-rule-strong pl-2 text-xs text-ink-faint"
          >
            <CornerUpLeft aria-hidden className="size-3 shrink-0" />
            <span className="font-[var(--weight-medium)] text-ink-soft">{quoted.name}</span>
            <span className="truncate">{quoted.body}</span>
          </p>
        )}
        {message.body && (
          <div className={cn("text-ink", message.streaming && "caret")}>
            <Markdown text={message.body} />
          </div>
        )}
        {message.attachments && message.attachments.length > 0 && (
          <ul className="mt-1.5 flex flex-wrap gap-1.5">
            {message.attachments.map((a) => (
              <li key={a.id}>
                <a
                  href={fileHref && !a.id.startsWith("local") ? fileHref(a.id) : undefined}
                  download={a.name}
                  aria-label={t("chat.download", { name: a.name })}
                  className="inline-flex items-center gap-1.5 rounded-xs border border-rule bg-surface px-2 py-1 text-xs text-ink-soft hover:bg-surface-sunk"
                >
                  <Paperclip aria-hidden className="size-3" />
                  <span className="max-w-48 truncate text-ink">{a.name}</span>
                  <span className="figures">{formatSize(a.size)}</span>
                </a>
              </li>
            ))}
          </ul>
        )}
        {message.card && <CardView card={message.card} onAnswer={(a) => onAnswer(message.card!.id, a)} />}
        {message.run && <RunLine run={message.run} onOpen={onOpenRun} active={activeRunId === message.run.run_id} />}
        {message.reactions && onReact && (
          <Reactions reactions={message.reactions} me={me ?? ""} onReact={(e) => onReact(message.id, e)} />
        )}
        {!message.streaming && !message.id.startsWith("local") && (onReply || onReact) && (
          <div className="absolute -top-2 right-0 flex h-7 items-center gap-0.5 rounded-sm border border-rule bg-surface-raised px-0.5 opacity-0 shadow-[var(--shadow-1)] transition-opacity duration-[var(--dur-1)] group-focus-within:opacity-100 group-hover:opacity-100">
            {onReply && (
              <button
                type="button"
                onClick={() => onReply(message)}
                className="inline-flex h-6 items-center gap-1 rounded-xs px-1.5 text-xs text-ink-faint hover:bg-surface-sunk hover:text-ink"
              >
                <CornerUpLeft aria-hidden className="size-3" />
                {t("chat.reply")}
              </button>
            )}
            {onReact && (
              <span role="group" aria-label={t("chat.react")} className="inline-flex items-center">
                <SmilePlus aria-hidden className="mx-1 size-3 text-ink-faint" />
                {QUICK.map((e) => (
                  <button
                    key={e}
                    type="button"
                    onClick={() => onReact(message.id, e)}
                    aria-label={t("chat.reactWith", { emoji: e })}
                    className="inline-flex size-6 items-center justify-center rounded-xs text-xs hover:bg-surface-sunk"
                  >
                    {e}
                  </button>
                ))}
              </span>
            )}
          </div>
        )}
      </div>
    </motion.article>
  );
});
