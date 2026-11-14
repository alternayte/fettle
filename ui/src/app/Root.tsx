import { useMemo } from "react";
import { Navigate, Outlet, useRouterState } from "@tanstack/react-router";
import { KeyRound, RotateCw } from "lucide-react";
import { errorText, isUnauthorized } from "@/api/client";
import { SessionContext, useBots, useMe, useSession } from "@/api/hooks";
import { Btn } from "@/components/form";
import { Toaster } from "@/components/ui/sonner";
import { t } from "@/lib/copy";
import { useTheme } from "@/lib/theme";

function FullPage({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-full items-center justify-center bg-ground px-4 py-12">
      <div className="w-full max-w-md">
        <p className="serif-title text-xl text-ink">{t("app.name")}</p>
        <div className="mt-4 border-t border-rule-strong pt-4">{children}</div>
      </div>
    </div>
  );
}

function SignedOut() {
  return (
    <FullPage>
      <div className="flex items-start gap-3">
        <KeyRound aria-hidden className="mt-1 size-5 shrink-0 text-ink-faint" />
        <div>
          <h1 className="serif-title text-2xl text-ink">{t("auth.title")}</h1>
          <p className="mt-2 text-base text-ink-soft">{t("auth.body")}</p>
          <p className="mt-3 rounded-sm border border-rule bg-surface px-3 py-2 font-mono text-sm text-ink">
            fettle up
          </p>
        </div>
      </div>
    </FullPage>
  );
}

function Failed({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <FullPage>
      <h1 className="serif-title text-2xl text-ink">{t("auth.failed")}</h1>
      <p className="mt-2 text-sm break-words text-ink-soft">{errorText(error)}</p>
      <Btn className="mt-4" onClick={onRetry}>
        <RotateCw aria-hidden className="size-3.5" />
        {t("common.retry")}
      </Btn>
    </FullPage>
  );
}

/** Root loads the caller and provides the session to every route. */
export function Root() {
  useTheme();
  const me = useMe();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const session = useMemo(() => {
    const m = me.data;
    const space = m?.spaces?.[0];
    if (!m || !space) return null;
    return { me: m, space, isOwner: space.role === "owner" };
  }, [me.data]);

  if (import.meta.env.DEV && pathname.startsWith("/__preview")) return <Outlet />;

  let body: React.ReactNode;
  if (me.isPending) body = <div className="h-full bg-ground" aria-busy="true" />;
  else if (me.isError && isUnauthorized(me.error)) body = <SignedOut />;
  else if (me.isError) body = <Failed error={me.error} onRetry={() => void me.refetch()} />;
  else if (!session)
    body = (
      <FullPage>
        <h1 className="serif-title text-2xl text-ink">{t("auth.noSpace")}</h1>
      </FullPage>
    );
  else
    body = (
      <SessionContext.Provider value={session}>
        <Outlet />
      </SessionContext.Provider>
    );
  return (
    <>
      {body}
      <Toaster position="bottom-right" />
    </>
  );
}

/** Index sends the caller to setup or to the chat of the primary bot. */
export function Index() {
  const { me } = useSession();
  const bots = useBots();
  if (me.setup.needs_model || me.setup.needs_name) return <Navigate to="/setup" replace />;
  if (bots.isPending) return <div className="h-full bg-ground" aria-busy="true" />;
  const list = bots.data ?? [];
  const primary = list.find((b) => b.is_primary) ?? list[0];
  if (!primary) return <Navigate to="/runs" replace />;
  return <Navigate to="/chat/$chatId" params={{ chatId: primary.chat_id }} replace />;
}
