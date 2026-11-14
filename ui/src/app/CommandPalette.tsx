import { useEffect } from "react";
import { Activity, FileText, Moon, Repeat, Settings, Sun } from "lucide-react";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "@/components/ui/command";
import { t } from "@/lib/copy";
import { useTheme } from "@/lib/theme";
import { BotMark } from "@/features/chat/BotMark";
import type { Bot, ChatSummary } from "@/features/chat/types";
import type { NavTarget } from "./Sidebar";

export function useHotkey(key: string, fn: () => void) {
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === key) {
        e.preventDefault();
        fn();
      }
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [key, fn]);
}

export function CommandPalette({
  open,
  onOpenChange,
  bots,
  chats,
  onNavigate,
  isOwner,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  bots: Bot[];
  chats: ChatSummary[];
  onNavigate: (to: NavTarget) => void;
  isOwner: boolean;
}) {
  const { setPref } = useTheme();
  const go = (to: NavTarget) => {
    onOpenChange(false);
    onNavigate(to);
  };
  const botById = Object.fromEntries(bots.map((b) => [b.id, b]));
  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("palette.title")}
      description={t("palette.description")}
    >
      <CommandInput placeholder={t("palette.placeholder")} />
      <CommandList>
        <CommandEmpty>{t("palette.empty")}</CommandEmpty>
        <CommandGroup heading={t("shell.bots")}>
          {chats
            .filter((c) => c.kind === "direct")
            .map((c) => {
              const b = botById[c.bot_ids[0]];
              if (!b) return null;
              return (
                <CommandItem
                  key={c.id}
                  value={`${b.name} ${b.job}`}
                  onSelect={() => go({ kind: "chat", chatId: c.id })}
                >
                  <BotMark name={b.name} size="sm" />
                  <span>{b.name}</span>
                  <span className="truncate text-xs text-ink-faint">{b.job}</span>
                </CommandItem>
              );
            })}
        </CommandGroup>
        <CommandGroup heading={t("palette.go")}>
          <CommandItem onSelect={() => go({ kind: "page", page: "runs" })}>
            <Activity aria-hidden /> {t("shell.runs")}
          </CommandItem>
          <CommandItem onSelect={() => go({ kind: "page", page: "files" })}>
            <FileText aria-hidden /> {t("shell.files")}
          </CommandItem>
          <CommandItem onSelect={() => go({ kind: "page", page: "routines" })}>
            <Repeat aria-hidden /> {t("shell.routines")}
          </CommandItem>
          {isOwner && (
            <CommandItem onSelect={() => go({ kind: "page", page: "settings" })}>
              <Settings aria-hidden /> {t("shell.settings")}
              <CommandShortcut>⌘,</CommandShortcut>
            </CommandItem>
          )}
        </CommandGroup>
        <CommandGroup heading={t("palette.appearance")}>
          <CommandItem
            onSelect={() => {
              setPref("light");
              onOpenChange(false);
            }}
          >
            <Sun aria-hidden /> {t("palette.light")}
          </CommandItem>
          <CommandItem
            onSelect={() => {
              setPref("dark");
              onOpenChange(false);
            }}
          >
            <Moon aria-hidden /> {t("palette.dark")}
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
