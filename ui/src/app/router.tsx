import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  lazyRouteComponent,
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
    validateSearch: (s: Record<string, unknown>): { run?: string; card?: string } => ({
      run: optionalString(s.run),
      card: optionalString(s.card),
    }),
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
  const learningRoute = createRoute({
    getParentRoute: () => app,
    path: "/learning",
    component: lazyRouteComponent(() => import("@/features/learning/LearningPage"), "LearningPage"),
  });
  const routinesRoute = createRoute({
    getParentRoute: () => app,
    path: "/routines",
    component: lazyRouteComponent(() => import("@/features/routines/RoutinesPage"), "RoutinesPage"),
  });
  const skillsRoute = createRoute({
    getParentRoute: () => app,
    path: "/skills",
    component: lazyRouteComponent(() => import("@/features/skills/SkillsPage"), "SkillsPage"),
    validateSearch: (s: Record<string, unknown>): { from_run?: string } => ({ from_run: optionalString(s.from_run) }),
  });
  const skillRoute = createRoute({
    getParentRoute: () => app,
    path: "/skills/$skillId",
    component: lazyRouteComponent(() => import("@/features/skills/SkillsPage"), "SkillPage"),
  });
  const screenRoute = createRoute({
    getParentRoute: () => app,
    path: "/screen/$botId",
    component: lazyRouteComponent(() => import("@/features/screen/ScreenPage"), "ScreenPage"),
    validateSearch: (s: Record<string, unknown>): { card?: string } => ({ card: optionalString(s.card) }),
  });
  const teachRoute = createRoute({
    getParentRoute: () => app,
    path: "/teach/$teachId",
    component: lazyRouteComponent(() => import("@/features/teach/TeachPage"), "TeachPage"),
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
    component: lazyRouteComponent(() => import("@/features/settings/SettingsLayout"), "SettingsIndex"),
  });
  const sub = (path: string, load: () => Promise<Record<string, unknown>>, name: string) =>
    createRoute({
      getParentRoute: () => settings,
      path,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      component: lazyRouteComponent(load as () => Promise<any>, name),
    });
  const botsRoute = createRoute({
    getParentRoute: () => app,
    path: "/bots",
    component: lazyRouteComponent(() => import("@/features/bots/BotsPage"), "BotsPage"),
  });
  const filesRoute = createRoute({
    getParentRoute: () => app,
    path: "/files",
    component: lazyRouteComponent(() => import("@/features/files/FilesPage"), "FilesPage"),
  });
  const inviteRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/invite/$token",
    component: lazyRouteComponent(() => import("@/features/invite/InvitePage"), "InvitePage"),
  });
  const admin = createRoute({
    getParentRoute: () => app,
    path: "/admin",
    component: lazyRouteComponent(() => import("@/features/admin/AdminPage"), "AdminPage"),
  });

  const routes: AnyRoute[] = [
    indexRoute,
    setupRoute,
    inviteRoute,
    app.addChildren([
      chat,
      runs,
      run,
      routinesRoute,
      learningRoute,
      skillsRoute,
      skillRoute,
      screenRoute,
      teachRoute,
      botsRoute,
      bot,
      filesRoute,
      settings.addChildren([
        settingsIndex,
        sub("models", () => import("@/features/settings/ModelsPage"), "ModelsPage"),
        sub("computers", () => import("@/features/settings/ComputersPage"), "ComputersPage"),
        sub("vault", () => import("@/features/settings/VaultPage"), "VaultPage"),
        sub("connectors", () => import("@/features/settings/ConnectorsPage"), "ConnectorsPage"),
        sub("approvals", () => import("@/features/settings/ApprovalsPage"), "ApprovalsPage"),
        sub("space", () => import("@/features/settings/SpacePage"), "SpacePage"),
        sub("budgets", () => import("@/features/settings/BudgetsPage"), "BudgetsPage"),
        sub("members", () => import("@/features/settings/MembersPage"), "MembersPage"),
        sub("account", () => import("@/features/settings/AccountPage"), "AccountPage"),
        sub("usage", () => import("@/features/settings/UsagePage"), "UsagePage"),
        sub("audit", () => import("@/features/settings/AuditPage"), "AuditPage"),
        sub("tokens", () => import("@/features/settings/TokensPage"), "TokensPage"),
        sub("notifications", () => import("@/features/settings/NotificationsPage"), "NotificationsPage"),
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
