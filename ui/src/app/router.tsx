import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  lazyRouteComponent,
  redirect,
  type AnyRoute,
} from "@tanstack/react-router";
import { ChatPage } from "@/features/chat/ChatPage";
import { t } from "@/lib/copy";
import { AppLayout } from "./AppLayout";
import { Index, Root } from "./Root";

function NotFound() {
  return (
    <div className="flex min-h-full items-center justify-center bg-ground px-4 py-12">
      <div className="w-full max-w-md">
        <h1 className="serif-title text-2xl text-ink">{t("notFound.title")}</h1>
        <p className="mt-2 text-base text-ink-soft">{t("notFound.body")}</p>
        <Link to="/" className="mt-4 inline-block text-sm text-ink underline underline-offset-2">
          {t("notFound.home")}
        </Link>
      </div>
    </div>
  );
}

const rootRoute = createRootRoute({ component: Root, notFoundComponent: NotFound });

const optionalString = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);

export async function createAppRouter() {
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Index });
  const setupRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/setup",
    component: lazyRouteComponent(() => import("@/features/setup/SetupPage"), "SetupPage"),
  });
  const app = createRoute({ getParentRoute: () => rootRoute, id: "app", component: AppLayout });
  const chat = createRoute({
    getParentRoute: () => app,
    path: "/chat/$chatId",
    component: ChatPage,
    validateSearch: (s: Record<string, unknown>): { run?: string } => ({ run: optionalString(s.run) }),
  });
  const runs = createRoute({
    getParentRoute: () => app,
    path: "/runs",
    component: lazyRouteComponent(() => import("@/features/runs/RunsPage"), "RunsPage"),
  });
  const run = createRoute({
    getParentRoute: () => app,
    path: "/runs/$runId",
    component: lazyRouteComponent(() => import("@/features/runs/RunsPage"), "RunPage"),
  });
  const bot = createRoute({
    getParentRoute: () => app,
    path: "/bots/$botId",
    component: lazyRouteComponent(() => import("@/features/bots/BotPage"), "BotPage"),
  });
  const settings = createRoute({
    getParentRoute: () => app,
    path: "/settings",
    component: lazyRouteComponent(() => import("@/features/settings/SettingsLayout"), "SettingsLayout"),
  });
  const settingsIndex = createRoute({
    getParentRoute: () => settings,
    path: "/",
    beforeLoad: () => {
      throw redirect({ to: "/settings/models", replace: true });
    },
  });
  const sub = (path: string, load: () => Promise<Record<string, unknown>>, name: string) =>
    createRoute({
      getParentRoute: () => settings,
      path,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      component: lazyRouteComponent(load as () => Promise<any>, name),
    });
  const admin = createRoute({
    getParentRoute: () => app,
    path: "/admin",
    component: lazyRouteComponent(() => import("@/features/admin/AdminPage"), "AdminPage"),
  });

  const routes: AnyRoute[] = [
    indexRoute,
    setupRoute,
    app.addChildren([
      chat,
      runs,
      run,
      bot,
      settings.addChildren([
        settingsIndex,
        sub("models", () => import("@/features/settings/ModelsPage"), "ModelsPage"),
        sub("computers", () => import("@/features/settings/ComputersPage"), "ComputersPage"),
        sub("vault", () => import("@/features/settings/VaultPage"), "VaultPage"),
        sub("approvals", () => import("@/features/settings/ApprovalsPage"), "ApprovalsPage"),
        sub("space", () => import("@/features/settings/SpacePage"), "SpacePage"),
        sub("usage", () => import("@/features/settings/UsagePage"), "UsagePage"),
        sub("audit", () => import("@/features/settings/AuditPage"), "AuditPage"),
        sub("tokens", () => import("@/features/settings/TokensPage"), "TokensPage"),
      ]),
      admin,
    ]),
  ];
  if (import.meta.env.DEV) {
    const { previewRoutes } = await import("@/features/preview/routes");
    routes.push(...previewRoutes(rootRoute));
  }
  return createRouter({ routeTree: rootRoute.addChildren(routes), defaultPreload: "intent" });
}
