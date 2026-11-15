import { useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useCreateGroup } from "@/api/hooks";
import { Btn, inputCls } from "@/components/form";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { BotMark } from "@/features/chat/BotMark";
import type { Bot } from "@/features/chat/types";
import { t, tn } from "@/lib/copy";

const MIN = 2;
const MAX = 6;

/** NewGroup makes a group chat of 2 to 6 bots (§6.2). */
export function NewGroup({ bots, onCreated }: { bots: Bot[]; onCreated: (chatId: string) => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const create = useCreateGroup();
  const active = bots.filter((b) => b.status !== "archived");
  const ok = name.trim() !== "" && picked.length >= MIN && picked.length <= MAX;
  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) {
          setName("");
          setPicked([]);
        }
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("groups.new")}
          className="inline-flex size-6 items-center justify-center rounded-xs text-ink-faint hover:bg-surface-sunk hover:text-ink"
        >
          <Plus aria-hidden className="size-3.5" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-3">
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (!ok) return;
            create.mutate(
              { name: name.trim(), bot_ids: picked },
              {
                onSuccess: (g) => {
                  setOpen(false);
                  onCreated(g.id);
                },
                onError: (err) => toast.error(t("groups.failed"), { description: errorText(err) }),
              },
            );
          }}
        >
          <div>
            <label htmlFor="group-name" className="text-sm font-[var(--weight-medium)] text-ink">
              {t("groups.name")}
            </label>
            <input
              id="group-name"
              value={name}
              maxLength={60}
              onChange={(e) => setName(e.target.value)}
              className={inputCls}
            />
          </div>
          <fieldset>
            <legend className="text-sm font-[var(--weight-medium)] text-ink">{t("groups.bots")}</legend>
            <p className="text-xs text-ink-faint">{t("groups.botsHint", { min: MIN, max: MAX })}</p>
            <ul className="mt-1.5 max-h-56 space-y-0.5 overflow-y-auto">
              {active.map((b) => {
                const on = picked.includes(b.id);
                return (
                  <li key={b.id}>
                    <label className="flex h-8 items-center gap-2 rounded-sm px-1 text-sm text-ink hover:bg-surface-sunk">
                      <input
                        type="checkbox"
                        checked={on}
                        disabled={!on && picked.length >= MAX}
                        onChange={() => setPicked((p) => (on ? p.filter((x) => x !== b.id) : [...p, b.id]))}
                        className="accent-[var(--accent)]"
                      />
                      <BotMark name={b.name} size="sm" />
                      <span className="truncate">{b.name}</span>
                    </label>
                  </li>
                );
              })}
            </ul>
          </fieldset>
          <div className="flex items-center justify-between">
            <span className="figures text-xs text-ink-faint">{tn("groups.picked", picked.length)}</span>
            <Btn type="submit" size="sm" kind="primary" disabled={!ok} busy={create.isPending}>
              {t("groups.create")}
            </Btn>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  );
}
