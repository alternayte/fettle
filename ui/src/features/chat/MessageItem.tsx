import { memo } from "react";
import { motion, useReducedMotion } from "motion/react";
import { Paperclip } from "lucide-react";
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

export const MessageItem = memo(function MessageItem({
  message,
  bot,
  continued,
  activeRunId,
  onOpenRun,
  onAnswer,
}: {
  message: Message;
  bot?: Bot;
  continued: boolean;
  activeRunId?: string;
  onOpenRun: (runId: string) => void;
  onAnswer: (cardId: string, a: CardAnswer) => void;
}) {
  const reduce = useReducedMotion();
  const isUser = message.author_kind === "user";
  const isSystem = message.author_kind === "system";
  const name = isUser ? t("chat.you") : (bot?.name ?? t("chat.unknownBot"));

  if (isSystem) {
    return (
      <div className="grid grid-cols-[3.25rem_1fr] gap-x-3 py-1 text-xs text-ink-faint sm:grid-cols-[4rem_1fr]">
        <span className="figures pt-px text-right">{formatClock(message.created_at)}</span>
        <span>{message.body}</span>
      </div>
    );
  }

  return (
    <motion.article
      initial={reduce || !message.streaming ? false : { opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.16 }}
      aria-label={t("chat.messageFrom", { name })}
      className={cn("group grid grid-cols-[3.25rem_1fr] gap-x-3 sm:grid-cols-[4rem_1fr]", continued ? "pt-1" : "pt-4")}
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
        {message.body && (
          <div className={cn("text-ink", message.streaming && "caret")}>
            <Markdown text={message.body} />
          </div>
        )}
        {message.attachments && message.attachments.length > 0 && (
          <ul className="mt-1.5 flex flex-wrap gap-1.5">
            {message.attachments.map((a) => (
              <li
                key={a.id}
                className="inline-flex items-center gap-1.5 rounded-xs border border-rule bg-surface px-2 py-1 text-xs text-ink-soft"
              >
                <Paperclip aria-hidden className="size-3" />
                <span className="max-w-48 truncate text-ink">{a.name}</span>
                <span className="figures">{formatSize(a.size)}</span>
              </li>
            ))}
          </ul>
        )}
        {message.card && <CardView card={message.card} onAnswer={(a) => onAnswer(message.card!.id, a)} />}
        {message.run && <RunLine run={message.run} onOpen={onOpenRun} active={activeRunId === message.run.run_id} />}
      </div>
    </motion.article>
  );
});
