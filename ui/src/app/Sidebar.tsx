import {
  Activity,
  AlarmClock,
  BookOpen,
  Hourglass,
  Loader,
  Monitor,
  Moon,
  Search,
  Settings,
  Sparkles,
  Sun,
  Users,
} from "lucide-react";
import { t } from "@/lib/copy";
import { useTheme, type ThemePref } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { BotMark } from "@/features/chat/BotMark";
import { NewGroup } from "./NewGroup";
import type { Bot, ChatSummary } from "@/features/chat/types";

export type NavTarget =
  { kind: "chat"; chatId: string } | { kind: "page"; page: "runs" | "routines" | "skills" | "settings" };

function Row({
  active,
  onClick,
  children,
  label,
}: {
  active?: boolean;
  onClick: () => void;
  children: React.ReactNode;
  label?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      aria-label={label}
      className={cn(
        "relative flex h-[var(--row)] w-full items-center gap-2 rounded-sm px-2 text-left text-sm text-ink-soft",
        "transition-colors duration-[var(--dur-1)] hover:bg-surface-sunk hover:text-ink",
        active && "bg-surface-sunk font-[var(--weight-medium)] text-ink",
      )}
    >
      {active && <span aria-hidden className="absolute top-1.5 bottom-1.5 left-0 w-[2px] rounded-full bg-ink" />}
      {children}
    </button>
  );
}

function BotState({ bot }: { bot: Bot }) {
  if (bot.status === "working")
    return (
      <span className="ml-auto inline-flex items-center gap-1 text-2xs text-info">
        <Loader aria-hidden className="size-3 animate-spin [animation-duration:2s]" />
        <span className="sr-only">{t("bots.status.working")}</span>
      </span>
    );
  if (bot.status === "waiting")
    return (
      <span className="ml-auto inline-flex items-center gap-1 text-2xs text-warn">
        <Hourglass aria-hidden className="size-3" />
        <span className="sr-only">{t("bots.status.waiting")}</span>
      </span>
    );
  return null;
}

const themeOrder: ThemePref[] = ["system", "light", "dark"];
const themeIcon = { system: Monitor, light: Sun, dark: Moon } as const;

export function Sidebar({
  spaceName,
  bots,
  chats,
  active,
  onNavigate,
  onPalette,
  isOwner,
}: {
  spaceName: string;
  bots: Bot[];
  chats: ChatSummary[];
  active: NavTarget | null;
  onNavigate: (to: NavTarget) => void;
  onPalette: () => void;
  isOwner: boolean;
}) {
  const { pref, setPref } = useTheme();
  const ThemeIcon = themeIcon[pref];
  const direct = chats.filter((c) => c.kind === "direct");
  const groups = chats.filter((c) => c.kind === "group");
  const botById = Object.fromEntries(bots.map((b) => [b.id, b]));
  const isChat = (id: string) => active?.kind === "chat" && active.chatId === id;
  const isPage = (p: string) => active?.kind === "page" && active.page === p;

  return (
    <nav aria-label={t("shell.nav")} className="flex h-full w-full flex-col bg-surface">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-rule px-3">
        <span
          aria-hidden
          className="inline-flex size-6 items-center justify-center rounded-xs bg-ink font-serif text-xs text-surface"
        >
          F
        </span>
        <span className="truncate text-sm font-[var(--weight-strong)] text-ink">{spaceName}</span>
      </div>
      <div className="px-2 pt-2">
        <button
          type="button"
          onClick={onPalette}
          className="flex h-8 w-full items-center gap-2 rounded-sm border border-rule bg-ground px-2 text-sm text-ink-faint hover:border-rule-strong hover:text-ink-soft"
        >
          <Search aria-hidden className="size-3.5" />
          <span>{t("shell.search")}</span>
          <kbd className="figures ml-auto rounded-xs border border-rule px-1 text-ink-faint">⌘K</kbd>
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        <h2 className="label-caps px-2 pt-4 pb-1">{t("shell.bots")}</h2>
        <ul>
          {direct.map((c) => {
            const bot = botById[c.bot_ids[0]];
            if (!bot) return null;
            return (
              <li key={c.id}>
                <Row active={isChat(c.id)} onClick={() => onNavigate({ kind: "chat", chatId: c.id })}>
                  <BotMark name={bot.name} size="sm" />
                  <span className="truncate">{bot.name}</span>
                  {bot.is_primary && (
                    <Sparkles aria-label={t("bots.primary")} className="size-3 shrink-0 text-ink-faint" />
                  )}
                  <BotState bot={bot} />
                  {c.unread > 0 && (
                    <span className="figures ml-auto rounded-xs bg-ink px-1 text-surface">
                      {c.unread}
                      <span className="sr-only"> {t("shell.unread")}</span>
                    </span>
                  )}
                </Row>
              </li>
            );
          })}
        </ul>
        {bots.length >= 2 && (
          <>
            <div className="flex items-center justify-between px-2 pt-4 pb-1">
              <h2 className="label-caps">{t("shell.groups")}</h2>
              <NewGroup bots={bots} onCreated={(id) => onNavigate({ kind: "chat", chatId: id })} />
            </div>
            <ul>
              {groups.map((c) => (
                <li key={c.id}>
                  <Row active={isChat(c.id)} onClick={() => onNavigate({ kind: "chat", chatId: c.id })}>
                    <Users aria-hidden className="size-4 shrink-0 text-ink-faint" />
                    <span className="truncate">{c.name}</span>
                    <span className="figures ml-auto text-ink-faint">{c.bot_ids.length}</span>
                  </Row>
                </li>
              ))}
            </ul>
          </>
        )}
        <h2 className="label-caps px-2 pt-4 pb-1">{t("shell.workspace")}</h2>
        <ul>
          <li>
            <Row active={isPage("runs")} onClick={() => onNavigate({ kind: "page", page: "runs" })}>
              <Activity aria-hidden className="size-4 text-ink-faint" />
              {t("shell.runs")}
            </Row>
          </li>
          <li>
            <Row active={isPage("routines")} onClick={() => onNavigate({ kind: "page", page: "routines" })}>
              <AlarmClock aria-hidden className="size-4 text-ink-faint" />
              {t("shell.routines")}
            </Row>
          </li>
          <li>
            <Row active={isPage("skills")} onClick={() => onNavigate({ kind: "page", page: "skills" })}>
              <BookOpen aria-hidden className="size-4 text-ink-faint" />
              {t("shell.skills")}
            </Row>
          </li>
          {isOwner && (
            <li>
              <Row active={isPage("settings")} onClick={() => onNavigate({ kind: "page", page: "settings" })}>
                <Settings aria-hidden className="size-4 text-ink-faint" />
                {t("shell.settings")}
              </Row>
            </li>
          )}
        </ul>
      </div>
      <div className="flex shrink-0 items-center gap-1 border-t border-rule px-2 py-1.5">
        <button
          type="button"
          onClick={() => setPref(themeOrder[(themeOrder.indexOf(pref) + 1) % themeOrder.length])}
          className="inline-flex h-7 items-center gap-1.5 rounded-sm px-2 text-xs text-ink-faint hover:bg-surface-sunk hover:text-ink"
          aria-label={t("shell.theme", { theme: t(`shell.themes.${pref}`) })}
        >
          <ThemeIcon aria-hidden className="size-3.5" />
          {t(`shell.themes.${pref}`)}
        </button>
      </div>
    </nav>
  );
}
