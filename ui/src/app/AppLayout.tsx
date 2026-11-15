import { useCallback, useMemo, useRef, useState } from "react";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { CircleAlert } from "lucide-react";
import { errorText } from "@/api/client";
import { keys, useBots, useChats, useRun, useSession } from "@/api/hooks";
import { useEventStream } from "@/api/sse";
import { Skeleton } from "@/components/ui/skeleton";
import { toBot, toChat } from "@/features/chat/map";
import { Timeline } from "@/features/runs/Timeline";
import { toRunDetail } from "@/features/runs/map";
import { t } from "@/lib/copy";
import { CommandPalette, useHotkey } from "./CommandPalette";
import { Notice } from "./Page";
import { Shell } from "./Shell";
import { ShellContext } from "./shell-context";
import { Sidebar, type NavTarget } from "./Sidebar";

function RunPanel({ runId, onClose }: { runId: string; onClose: () => void }) {
  const q = useRun(runId);
  const detail = useMemo(() => (q.data ? toRunDetail(q.data) : null), [q.data]);
  if (detail) return <Timeline run={detail} onClose={onClose} />;
  return (
    <div className="flex h-full flex-col bg-ground">
      <header className="shrink-0 space-y-2 border-b border-rule bg-surface px-4 py-3">
        <p className="label-caps">{t("timeline.title")}</p>
        {q.isError ? null : <Skeleton className="h-5 w-2/3 bg-surface-sunk" />}
      </header>
      <div className="p-4">
        {q.isError ? (
          <Notice icon={CircleAlert} tone="danger" title={t("timeline.loadFailed")}>
            {errorText(q.error)}
          </Notice>
        ) : (
          <p className="text-sm text-ink-faint" role="status">
            {t("common.loading")}
          </p>
        )}
      </div>
    </div>
  );
}

/** AppLayout is the signed-in frame: sidebar, command menu, main page and the run panel. */
export function AppLayout() {
  const { space, isOwner } = useSession();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [menu, setMenu] = useState(false);
  const [palette, setPalette] = useState(false);
  const bots = useBots();
  const chats = useChats();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const runParam = useRouterState({ select: (s) => (s.location.search as { run?: string }).run });

  const uiBots = useMemo(() => (bots.data ?? []).filter((b) => b.status !== "archived").map(toBot), [bots.data]);
  const uiChats = useMemo(() => (chats.data ?? []).map(toChat), [chats.data]);

  // Bot activity follows the run events of the whole space. Bursts refresh the list once.
  const pendingRefresh = useRef<ReturnType<typeof setTimeout> | null>(null);
  const refreshActivity = useCallback(() => {
    if (pendingRefresh.current) return;
    pendingRefresh.current = setTimeout(() => {
      pendingRefresh.current = null;
      void qc.invalidateQueries({ queryKey: keys.bots(space.id), exact: true });
      void qc.invalidateQueries({ queryKey: keys.runs(space.id) });
    }, 250);
  }, [qc, space.id]);
  const spaceHandlers = useMemo(() => ({ run: refreshActivity, bot: refreshActivity }), [refreshActivity]);
  useEventStream(`/api/v1/spaces/${space.id}/stream`, spaceHandlers, refreshActivity);

  const active: NavTarget | null = pathname.startsWith("/chat/")
    ? { kind: "chat", chatId: pathname.split("/")[2] ?? "" }
    : pathname.startsWith("/runs")
      ? { kind: "page", page: "runs" }
      : pathname.startsWith("/routines")
        ? { kind: "page", page: "routines" }
        : pathname.startsWith("/skills")
          ? { kind: "page", page: "skills" }
          : pathname.startsWith("/learning")
            ? { kind: "page", page: "learning" }
            : pathname.startsWith("/settings") || pathname.startsWith("/admin")
              ? { kind: "page", page: "settings" }
              : null;

  const onNavigate = useCallback(
    (to: NavTarget) => {
      setMenu(false);
      if (to.kind === "chat") void navigate({ to: "/chat/$chatId", params: { chatId: to.chatId } });
      else if (to.page === "runs") void navigate({ to: "/runs" });
      else if (to.page === "routines") void navigate({ to: "/routines" });
      else if (to.page === "skills") void navigate({ to: "/skills" });
      else if (to.page === "learning") void navigate({ to: "/learning" });
      else void navigate({ to: "/settings/models" });
    },
    [navigate],
  );

  useHotkey(
    "k",
    useCallback(() => setPalette((o) => !o), []),
  );
  useHotkey(
    ",",
    useCallback(() => {
      if (isOwner) void navigate({ to: "/settings/models" });
    }, [isOwner, navigate]),
  );

  const closePanel = useCallback(
    () => void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...prev, run: undefined }) }),
    [navigate],
  );

  const shell = useMemo(() => ({ openMenu: () => setMenu(true), openPalette: () => setPalette(true) }), []);
  const showPanel = pathname.startsWith("/chat/") && !!runParam;

  return (
    <ShellContext.Provider value={shell}>
      <Shell
        sidebar={
          <Sidebar
            spaceName={space.name}
            bots={uiBots}
            chats={uiChats}
            active={active}
            onNavigate={onNavigate}
            onPalette={() => {
              setMenu(false);
              setPalette(true);
            }}
            isOwner={isOwner}
          />
        }
        menuOpen={menu}
        onMenuOpenChange={setMenu}
        panel={showPanel ? <RunPanel key={runParam} runId={runParam!} onClose={closePanel} /> : null}
      >
        <Outlet />
      </Shell>
      <CommandPalette
        open={palette}
        onOpenChange={setPalette}
        bots={uiBots}
        chats={uiChats}
        onNavigate={onNavigate}
        isOwner={isOwner}
      />
    </ShellContext.Provider>
  );
}
