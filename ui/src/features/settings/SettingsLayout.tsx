import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import { Lock } from "lucide-react";
import { useSession } from "@/api/hooks";
import { Notice, Page } from "@/app/Page";
import { t } from "@/lib/copy";

const tabs = ["models", "computers", "connectors", "vault", "approvals", "space", "usage", "audit"] as const;

const tabCls =
  "inline-flex h-8 shrink-0 items-center whitespace-nowrap border-b-2 border-transparent px-2.5 text-sm text-ink-soft hover:text-ink aria-[current=page]:border-ink aria-[current=page]:font-[var(--weight-medium)] aria-[current=page]:text-ink";

export function SettingsNav() {
  const { me, isOwner } = useSession();
  return (
    <nav
      aria-label={t("settings.nav")}
      className="-mx-4 mb-6 overflow-x-auto border-b border-rule px-4 sm:-mx-8 sm:px-8"
    >
      <ul className="flex gap-1">
        {isOwner &&
          tabs.map((tab) => (
            <li key={tab}>
              <Link to={`/settings/${tab}`} className={tabCls} activeProps={{ "aria-current": "page" }}>
                {t(`settings.tabs.${tab}`)}
              </Link>
            </li>
          ))}
        <li>
          <Link to="/settings/notifications" className={tabCls} activeProps={{ "aria-current": "page" }}>
            {t("settings.tabs.notifications")}
          </Link>
        </li>
        {me.mode === "server" && (
          <li>
            <Link to="/settings/tokens" className={tabCls} activeProps={{ "aria-current": "page" }}>
              {t("settings.tabs.tokens")}
            </Link>
          </li>
        )}
        {me.is_admin && (
          <li>
            <Link to="/admin" className={tabCls} activeProps={{ "aria-current": "page" }}>
              {t("settings.tabs.admin")}
            </Link>
          </li>
        )}
      </ul>
    </nav>
  );
}

export function SettingsLayout() {
  const { isOwner } = useSession();
  const pathname = useRouterState({ select: (st) => st.location.pathname });
  // Access tokens belong to the caller, so members open them too.
  const own = pathname.startsWith("/settings/tokens") || pathname.startsWith("/settings/notifications");
  return (
    <Page title={t("shell.settings")} wide>
      <SettingsNav />
      {isOwner || own ? (
        <Outlet />
      ) : (
        <Notice icon={Lock} title={t("settings.ownerOnly.title")}>
          {t("settings.ownerOnly.body")}
        </Notice>
      )}
    </Page>
  );
}
