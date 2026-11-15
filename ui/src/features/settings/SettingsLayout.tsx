import { Link, Navigate, Outlet, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
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

// One row of tabs that scrolls on narrow screens; a grouped list beside the page on wide ones.
const tabCls =
  "inline-flex h-8 shrink-0 items-center whitespace-nowrap border-b-2 border-transparent px-2.5 text-sm text-ink-soft hover:text-ink aria-[current=page]:border-ink aria-[current=page]:font-[var(--weight-medium)] aria-[current=page]:text-ink xl:w-full xl:rounded-sm xl:border-b-0 xl:px-3 xl:hover:bg-surface-sunk xl:aria-[current=page]:bg-surface-sunk";

export function SettingsNav() {
  const { me, isOwner } = useSession();
  const pathname = useRouterState({ select: (st) => st.location.pathname });
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    ref.current?.querySelector('[aria-current="page"]')?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [pathname]);
  const groups: { key: string; links: { to: string; label: string }[] }[] = [];
  if (isOwner) {
    groups.push({
      key: "space",
      links: OWNER_TABS.map((tab) => ({ to: `/settings/${tab}`, label: t(`settings.tabs.${tab}`) })),
    });
  }
  groups.push({
    key: "you",
    links: MEMBER_TABS.map((tab) => ({ to: `/settings/${tab}`, label: t(`settings.tabs.${tab}`) })),
  });
  if (me.is_admin) groups.push({ key: "server", links: [{ to: "/admin", label: t("settings.tabs.admin") }] });
  return (
    <nav
      ref={ref}
      aria-label={t("settings.nav")}
      className="-mx-4 mb-6 flex overflow-x-auto border-b border-rule px-4 [mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)] sm:-mx-8 sm:px-8 xl:sticky xl:top-0 xl:mx-0 xl:mb-0 xl:block xl:self-start xl:overflow-visible xl:border-b-0 xl:px-0 xl:[mask-image:none]"
    >
      {groups.map((g) => (
        <div key={g.key} className="flex shrink-0 xl:mb-5 xl:block">
          <p className="hidden px-3 pb-1 text-xs font-[var(--weight-medium)] tracking-wide text-ink-faint uppercase xl:block">
            {t(`settings.groups.${g.key}`)}
          </p>
          <ul className="flex gap-1 xl:flex-col xl:gap-0.5">
            {g.links.map((l) => (
              <li key={l.to}>
                <Link to={l.to} className={tabCls} activeProps={{ "aria-current": "page" }}>
                  {l.label}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

/** SettingsFrame puts the settings menu beside or above a settings page. */
export function SettingsFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="xl:grid xl:grid-cols-[11rem_minmax(0,1fr)] xl:gap-10">
      <SettingsNav />
      <div className="min-w-0">{children}</div>
    </div>
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
      <SettingsFrame>
        {isOwner || own ? (
          <Outlet />
        ) : (
          <Notice icon={Lock} title={t("settings.ownerOnly.title")}>
            {t("settings.ownerOnly.body")}
          </Notice>
        )}
      </SettingsFrame>
    </Page>
  );
}
