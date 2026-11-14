import { createRoute, type AnyRoute } from "@tanstack/react-router";
import { Wizard } from "@/features/setup/Wizard";
import { Timeline } from "@/features/runs/Timeline";
import { PreviewChat } from "./PreviewChat";
import * as fx from "./fixtures";

// Design preview routes (G1). Only the dev build imports this module.
export function previewRoutes(root: AnyRoute) {
  return [
    createRoute({ getParentRoute: () => root, path: "/__preview/chat", component: PreviewChat }),
    createRoute({
      getParentRoute: () => root,
      path: "/__preview/timeline",
      component: () => (
        <div className="mx-auto h-dvh max-w-2xl border-x border-rule">
          <Timeline run={fx.run} />
        </div>
      ),
    }),
    createRoute({
      getParentRoute: () => root,
      path: "/__preview/setup",
      component: () => <Wizard model={fx.wizard} />,
    }),
  ];
}
