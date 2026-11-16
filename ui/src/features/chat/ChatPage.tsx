import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { CircleAlert, GraduationCap, Monitor, MoreHorizontal, Settings2 } from "lucide-react";
import { uploadAttachment, useBots, useChats, useSession, useStopRun } from "@/api/hooks";
import { errorText } from "@/api/client";
import { Notice, Page } from "@/app/Page";
import { useShell } from "@/app/shell-context";
import { Btn } from "@/components/form";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { TeachDialog } from "@/features/teach/TeachDialog";
import { t } from "@/lib/copy";
import { attachmentHref } from "@/lib/format";
import { toast } from "sonner";
import { ChatView } from "./ChatView";
import { toBot } from "./map";
import { useChat } from "./useChat";

function Empty({ name, loaded, group }: { name: string; loaded: boolean; group?: boolean }) {
  if (!loaded)
    return (
      <p className="pt-10 text-center text-sm text-ink-faint" role="status">
        {t("common.loading")}
      </p>
    );
  return (
    <div className="pt-16 pb-4 text-center">
      <p className="serif-title text-2xl text-ink">{t("chat.empty.title", { name })}</p>
      <p className="mt-1.5 text-base text-ink-soft">{group ? t("chat.empty.groupBody") : t("chat.empty.body")}</p>
    </div>
  );
}

function ChatScreen({ chatId }: { chatId: string }) {
  const { me, space } = useSession();
  const shell = useShell();
  const navigate = useNavigate();
  const chats = useChats();
  const bots = useBots();
  const runParam = useRouterState({ select: (s) => (s.location.search as { run?: string }).run });
  const cardParam = useRouterState({ select: (s) => (s.location.search as { card?: string }).card });
  const c = useChat(space.id, chatId, me.id);
  const stop = useStopRun();
  const [teaching, setTeaching] = useState(false);

  const botMap = useMemo(() => Object.fromEntries((bots.data ?? []).map((b) => [b.id, toBot(b)])), [bots.data]);
  const chat = chats.data?.find((x) => x.id === chatId);
  const bot = chat?.kind === "direct" ? botMap[chat.bot_ids?.[0] ?? ""] : undefined;

  const setRun = useCallback(
    (id: string | undefined) =>
      void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...prev, run: id }), replace: true }),
    [navigate],
  );
  const openRun = useCallback((id: string) => setRun(runParam === id ? undefined : id), [runParam, setRun]);
  const lastRun = useMemo(() => {
    for (let i = c.messages.length - 1; i >= 0; i--) if (c.messages[i].run) return c.messages[i].run!.run_id;
    return undefined;
  }, [c.messages]);

  // A notification link opens the exact card (§15.2).
  const hasCard = !!cardParam && c.messages.some((m) => m.card?.id === cardParam);
  useEffect(() => {
    if (!hasCard) return;
    const el = document.querySelector<HTMLElement>(`[data-card-id="${CSS.escape(cardParam!)}"]`);
    el?.scrollIntoView({ block: "center" });
    el?.focus({ preventScroll: true });
  }, [hasCard, cardParam]);

  const streaming = c.messages.some((m) => m.streaming);
  const active = c.activeRun;
  const onStop = useCallback(() => {
    if (!active) return;
    stop.mutate(active.run_id, { onError: (err) => toast.error(t("run.stopFailed"), { description: errorText(err) }) });
  }, [active, stop]);

  if (chats.isSuccess && !chat) {
    return (
      <Page title={t("chat.missing.title")}>
        <p className="text-base text-ink-soft">{t("chat.missing.body")}</p>
      </Page>
    );
  }

  const title = chat?.name || bot?.name || "";
  const groupNames =
    chat?.kind === "group"
      ? (chat.bot_ids ?? [])
          .map((id) => botMap[id]?.name)
          .filter(Boolean)
          .join(", ")
      : undefined;
  const empty = c.error ? (
    <div className="pt-10">
      <Notice icon={CircleAlert} tone="danger" title={t("chat.loadFailed")}>
        <p>{c.error}</p>
        <Btn size="sm" className="mt-2" onClick={c.reload}>
          {t("common.retry")}
        </Btn>
      </Notice>
    </div>
  ) : (
    <Empty name={bot?.name ?? title} loaded={c.loaded} group={chat?.kind === "group"} />
  );

  return (
    <ChatView
      title={title}
      subtitle={bot?.job ?? groupNames}
      bot={bot}
      bots={botMap}
      messages={c.messages}
      running={!!active || streaming}
      onSend={c.send}
      onStop={active ? onStop : undefined}
      onAnswer={c.answer}
      onOpenRun={openRun}
      activeRunId={runParam}
      onMenu={shell.openMenu}
      onPanel={runParam || lastRun ? () => setRun(runParam ? undefined : lastRun) : undefined}
      panelOpen={!!runParam}
      onMemory={
        bot ? () => void navigate({ to: "/bots/$botId", params: { botId: bot.id }, hash: "memory" }) : undefined
      }
      me={me.id}
      members={chat?.kind === "group" ? (chat.bot_ids ?? []).map((id) => botMap[id]).filter(Boolean) : undefined}
      onUpload={(file) => uploadAttachment(space.id, chatId, file)}
      onReact={c.react}
      fileHref={(id) => attachmentHref(space.id, chatId, id)}
      onReachTop={c.hasOlder ? c.loadOlder : undefined}
      loadingOlder={c.loadingOlder}
      empty={empty}
      headerActions={
        bot ? (
          <>
            {bot.screen && (
              <Link
                to="/screen/$botId"
                params={{ botId: bot.id }}
                aria-label={t("chat.openScreen", { name: bot.name })}
                title={t("chat.openScreen", { name: bot.name })}
                className="inline-flex size-8 items-center justify-center rounded-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
              >
                <Monitor className="size-4" aria-hidden />
              </Link>
            )}
            <button
              type="button"
              onClick={() => void navigate({ to: "/bots/$botId", params: { botId: bot.id } })}
              aria-label={t("chat.botSettings", { name: bot.name })}
              className="inline-flex size-8 items-center justify-center rounded-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
            >
              <Settings2 className="size-4" aria-hidden />
            </button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  aria-label={t("chat.menu", { name: bot.name })}
                  className="inline-flex size-8 items-center justify-center rounded-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
                >
                  <MoreHorizontal className="size-4" aria-hidden />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setTeaching(true)}>
                  <GraduationCap aria-hidden className="size-3.5" />
                  {t("teach.action")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            {teaching && <TeachDialog botId={bot.id} botName={bot.name} open={teaching} onOpenChange={setTeaching} />}
          </>
        ) : undefined
      }
    />
  );
}

export function ChatPage() {
  const { chatId } = useParams({ strict: false }) as { chatId: string };
  return <ChatScreen key={chatId} chatId={chatId} />;
}
