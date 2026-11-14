import { createRootRoute, createRoute, createRouter, Outlet, redirect, type AnyRoute } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";

const rootRoute = createRootRoute({ component: () => <Outlet /> });

export async function createAppRouter(_queryClient: QueryClient) {
  const routes: AnyRoute[] = [
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      beforeLoad: () => {
        throw redirect({ to: "/__preview/chat" });
      },
    }),
  ];
  if (import.meta.env.DEV) {
    const { previewRoutes } = await import("@/features/preview/routes");
    routes.push(...previewRoutes(rootRoute));
  }
  return createRouter({ routeTree: rootRoute.addChildren(routes), defaultPreload: "intent" });
}
