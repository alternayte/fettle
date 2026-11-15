import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import { BookOpen, Loader, Menu, PanelRight } from "lucide-react";
import { t } from "@/lib/copy";
import { formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";
import { BotMark } from "./BotMark";
import type { CardAnswer } from "./CardView";
import { Composer } from "./Composer";
import { MessageItem } from "./MessageItem";
import type { Attachment, Bot, Message } from "./types";

const FIVE_MIN = 5 * 60 * 1000;

export function ChatHeader({
  title,
  subtitle,
  bot,
  onMenu,
  onPanel,
  panelOpen,
  onMemory,
  children,
}: {
  title: string;
  subtitle?: string;
  bot?: Bot;
  onMenu?: () => void;
  onPanel?: () => void;
  panelOpen?: boolean;
  onMemory?: () => void;
  /** Extra actions at the end of the header. */
  children?: React.ReactNode;
}) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-2.5 border-b border-rule bg-ground/90 px-3 backdrop-blur-sm sm:px-5">
      {onMenu && (
        <button
          type="button"
          onClick={onMenu}
          aria-label={t("shell.openMenu")}
          className="inline-flex size-8 items-center justify-center rounded-sm text-ink-soft hover:bg-surface-sunk md:hidden"
        >
          <Menu className="size-4" aria-hidden />
        </button>
      )}
      {bot && <BotMark name={bot.name} size="sm" />}
      <div className="flex min-w-0 items-baseline gap-2">
        <h1 className="truncate text-md font-[var(--weight-strong)] text-ink">{title}</h1>
        {subtitle && <p className="hidden truncate text-sm text-ink-faint sm:block">{subtitle}</p>}
      </div>
      <div className="ml-auto flex items-center gap-0.5">
        {children}
        {bot?.zone && (
          <span className="figures mr-2 hidden rounded-xs border border-rule px-1.5 py-0.5 text-ink-faint lg:inline">
            {t("chat.zone", { zone: bot.zone })}
          </span>
        )}
        {onMemory && (
          <button
            type="button"
            onClick={onMemory}
            aria-label={t("chat.memory")}
            className="inline-flex size-8 items-center justify-center rounded-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
          >
            <BookOpen className="size-4" aria-hidden />
          </button>
        )}
        {onPanel && (
          <button
            type="button"
            onClick={onPanel}
            aria-pressed={panelOpen}
            aria-label={t("shell.togglePanel")}
            className={cn(
              "inline-flex size-8 items-center justify-center rounded-sm text-ink-soft hover:bg-surface-sunk hover:text-ink",
              panelOpen && "bg-surface-sunk text-ink",
            )}
          >
            <PanelRight className="size-4" aria-hidden />
          </button>
        )}
      </div>
    </header>
  );
}

export function MessageList({
  messages,
  bots,
  activeRunId,
  onOpenRun,
  onAnswer,
  empty,
  onReachTop,
  loadingOlder,
  me,
  onReply,
  onReact,
  fileHref,
}: {
  messages: Message[];
  bots: Record<string, Bot>;
  activeRunId?: string;
  onOpenRun: (runId: string) => void;
  onAnswer: (cardId: string, a: CardAnswer) => void;
  empty?: React.ReactNode;
  /** Called when the reader scrolls near the oldest loaded message. */
  onReachTop?: () => void;
  loadingOlder?: boolean;
  me?: string;
  onReply?: (m: Message) => void;
  onReact?: (id: string, emoji: string) => void;
  fileHref?: (attachmentId: string) => string;
}) {
  const byId = new Map(messages.map((m) => [m.id, m]));
  const authorName = (m: Message) =>
    m.author_kind === "user" ? t("chat.you") : (bots[m.author_id]?.name ?? t("chat.unknownBot"));
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const reachTop = useRef(onReachTop);
  const firstId = useRef<string | undefined>(undefined);
  const lastHeight = useRef(0);

  useEffect(() => {
    reachTop.current = onReachTop;
  });

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const onScroll = () => {
      pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      if (el.scrollTop < 240) reachTop.current?.();
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const first = messages[0]?.id;
    if (pinned.current) el.scrollTop = el.scrollHeight;
    else if (firstId.current !== undefined && first !== firstId.current) {
      // Older messages were added above; keep the reader at the same message.
      el.scrollTop += el.scrollHeight - lastHeight.current;
    }
    firstId.current = first;
    lastHeight.current = el.scrollHeight;
  }, [messages]);

  return (
    <div
      ref={scroller}
      className="min-h-0 flex-1 overflow-y-auto overscroll-contain"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
    >
      <div className="mx-auto w-full max-w-[calc(var(--measure)+5rem)] px-3 pt-2 pb-6 sm:px-6">
        {loadingOlder && (
          <p className="flex items-center justify-center gap-1.5 py-2 text-xs text-ink-faint" role="status">
            <Loader aria-hidden className="size-3 animate-spin" />
            {t("chat.loadingOlder")}
          </p>
        )}
        {messages.length === 0 && empty}
        {messages.map((m, i) => {
          const prev = messages[i - 1];
          const newDay = !prev || formatDay(prev.created_at) !== formatDay(m.created_at);
          const continued =
            !newDay &&
            !!prev &&
            prev.author_kind === m.author_kind &&
            prev.author_id === m.author_id &&
            new Date(m.created_at).getTime() - new Date(prev.created_at).getTime() < FIVE_MIN &&
            !prev.card;
          return (
            <Fragment key={m.id}>
              {newDay && (
                <div className="mt-5 mb-1 flex items-center gap-3" role="separator">
                  <span className="label-caps">{formatDay(m.created_at)}</span>
                  <span className="h-px flex-1 bg-rule" />
                </div>
              )}
              <MessageItem
                message={m}
                bot={m.author_kind === "bot" ? bots[m.author_id] : undefined}
                continued={continued && !m.reply_to}
                activeRunId={activeRunId}
                onOpenRun={onOpenRun}
                onAnswer={onAnswer}
                quoted={(() => {
                  const q = m.reply_to ? byId.get(m.reply_to) : undefined;
                  return q ? { name: authorName(q), body: q.body } : undefined;
                })()}
                me={me}
                onReply={onReply}
                onReact={onReact}
                fileHref={fileHref}
              />
            </Fragment>
          );
        })}
      </div>
    </div>
  );
}

export function ChatView({
  title,
  subtitle,
  bot,
  bots,
  messages,
  running,
  onSend,
  onStop,
  onAnswer,
  onOpenRun,
  activeRunId,
  onMenu,
  onPanel,
  panelOpen,
  empty,
  onMemory,
  onReachTop,
  loadingOlder,
  headerActions,
  me,
  members,
  onUpload,
  onReact,
  fileHref,
}: {
  title: string;
  subtitle?: string;
  bot?: Bot;
  bots: Record<string, Bot>;
  messages: Message[];
  running: boolean;
  onSend: (text: string, o?: { replyTo?: string; attachments?: Attachment[] }) => void;
  onStop?: () => void;
  onAnswer: (cardId: string, a: CardAnswer) => void;
  onOpenRun: (runId: string) => void;
  activeRunId?: string;
  onMenu?: () => void;
  onPanel?: () => void;
  panelOpen?: boolean;
  empty?: React.ReactNode;
  onMemory?: () => void;
  onReachTop?: () => void;
  loadingOlder?: boolean;
  headerActions?: React.ReactNode;
  me?: string;
  /** The bots of a group chat, for @mentions. */
  members?: Bot[];
  onUpload?: (file: File) => Promise<Attachment>;
  onReact?: (id: string, emoji: string) => void;
  fileHref?: (attachmentId: string) => string;
}) {
  const [replyTo, setReplyTo] = useState<Message | undefined>(undefined);
  const replyName = replyTo
    ? replyTo.author_kind === "user"
      ? t("chat.you")
      : (bots[replyTo.author_id]?.name ?? t("chat.unknownBot"))
    : undefined;
  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col">
      <ChatHeader
        title={title}
        subtitle={subtitle}
        bot={bot}
        onMenu={onMenu}
        onPanel={onPanel}
        panelOpen={panelOpen}
        onMemory={onMemory}
      >
        {headerActions}
      </ChatHeader>
      <MessageList
        messages={messages}
        bots={bots}
        activeRunId={activeRunId}
        onOpenRun={onOpenRun}
        onAnswer={onAnswer}
        empty={empty}
        onReachTop={onReachTop}
        loadingOlder={loadingOlder}
        me={me}
        onReply={me ? setReplyTo : undefined}
        onReact={onReact}
        fileHref={fileHref}
      />
      <Composer
        botName={bot?.name ?? title}
        running={running}
        onSend={(text, attachments) => {
          onSend(text, { replyTo: replyTo?.id, attachments });
          setReplyTo(undefined);
        }}
        onStop={onStop}
        onUpload={onUpload}
        members={members}
        replyTo={replyTo}
        replyName={replyName}
        onCancelReply={() => setReplyTo(undefined)}
      />
    </section>
  );
}
