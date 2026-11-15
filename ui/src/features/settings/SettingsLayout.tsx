import { Link, Navigate, Outlet, useRouterState } from "@tanstack/react-router";
import { Lock } from "lucide-react";
import { useSession } from "@/api/hooks";
import { Notice, Page } from "@/app/Page";
import { t } from "@/lib/copy";

/** The settings pages that only an owner of the space opens. */
export const OWNER_TABS = [
  "models",
  "computers",
  "connectors",
  "vault",
  "approvals",
  "budgets",
  "usage",
  "members",
  "space",
  "audit",
] as const;

/** The settings pages of the caller's own account, which every member opens. */
export const MEMBER_TABS = ["account", "notifications", "tokens"] as const;

const tabCls =
  "inline-flex h-8 shrink-0 items-center whitespace-nowrap border-b-2 border-transparent px-2.5 text-sm text-ink-soft hover:text-ink aria-[current=page]:border-ink aria-[current=page]:font-[var(--weight-medium)] aria-[current=page]:text-ink";

export function SettingsNav() {
  const { me, isOwner } = useSession();
  const tabs = [...(isOwner ? OWNER_TABS : []), ...MEMBER_TABS];
  return (
    <nav
      aria-label={t("settings.nav")}
      className="-mx-4 mb-6 overflow-x-auto border-b border-rule px-4 sm:-mx-8 sm:px-8"
    >
      <ul className="flex gap-1">
        {tabs.map((tab) => (
          <li key={tab}>
            <Link to={`/settings/${tab}`} className={tabCls} activeProps={{ "aria-current": "page" }}>
              {t(`settings.tabs.${tab}`)}
            </Link>
          </li>
        ))}
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

/** SettingsIndex opens the first settings page that the caller can see. */
export function SettingsIndex() {
  const { isOwner } = useSession();
  return <Navigate to={isOwner ? "/settings/models" : "/settings/account"} replace />;
}

export function SettingsLayout() {
  const { isOwner } = useSession();
  const pathname = useRouterState({ select: (st) => st.location.pathname });
  const own =
    MEMBER_TABS.some((tab) => pathname.startsWith(`/settings/${tab}`)) || pathname.replace(/\/$/, "") === "/settings";
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
