import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Archive, Bot as BotIcon, Circle, CircleAlert, Hourglass, Loader, Plus, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useBots, useCreateBot, useCreateFromTemplate, useSession, useTemplates, useZones } from "@/api/hooks";
import type { SchemaBotView } from "@/api/schema";
import { Notice, Page } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { Skeleton } from "@/components/ui/skeleton";
import { BotMark } from "@/features/chat/BotMark";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import { NativeSelect } from "@/features/settings/ProviderForm";

/** BotStatus shows the state of a bot with an icon and a word. */
export function BotStatus({ bot, className }: { bot: SchemaBotView; className?: string }) {
  const state = bot.status === "archived" ? "archived" : bot.activity;
  const Icon = { working: Loader, waiting: Hourglass, idle: Circle, archived: Archive }[state] ?? Circle;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-sm",
        state === "working" && "text-info",
        state === "waiting" && "text-warn",
        (state === "idle" || state === "archived") && "text-ink-faint",
        className,
      )}
    >
      <Icon
        aria-hidden
        className={cn("size-3.5 shrink-0", state === "working" && "animate-spin [animation-duration:2s]")}
      />
      {t(`bots.status.${state}`)}
    </span>
  );
}

function NewBotForm({ onDone }: { onDone: () => void }) {
  const create = useCreateBot();
  const fromTemplate = useCreateFromTemplate();
  const templates = useTemplates();
  const zones = useZones();
  const navigate = useNavigate();
  const [source, setSource] = useState("own");
  const [name, setName] = useState("");
  const [job, setJob] = useState("");
  const [zoneId, setZoneId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const tmpl = (templates.data ?? []).find((x) => x.slug === source);
  const zoneList = zones.data ?? [];
  // A template suggests its own zone; your own bot starts in personal.
  const suggested = zoneList.find((z) => z.name === (tmpl ? tmpl.zone : "personal"))?.id ?? zoneList[0]?.id ?? "";
  const zone = zoneId || suggested;
  const busy = create.isPending || fromTemplate.isPending;
  const done = (b: SchemaBotView) => {
    toast.success(t("botsPage.created", { name: b.name }));
    onDone();
    void navigate({ to: "/bots/$botId", params: { botId: b.id } });
  };
  const failed = (err: unknown) => toast.error(t("botsPage.failed"), { description: errorText(err) });
  const sources = [
    { id: "own", name: t("botsPage.own"), job: t("botsPage.ownBody") },
    ...(templates.data ?? []).map((x) => ({ id: x.slug, name: x.name, job: x.job })),
  ];
  return (
    <form
      aria-label={t("botsPage.new")}
      className="mb-6 space-y-5 rounded-sm border border-rule bg-surface px-4 py-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (tmpl) {
          return fromTemplate.mutate(
            { slug: tmpl.slug, zone_id: zone || undefined },
            { onSuccess: done, onError: failed },
          );
        }
        if (!name.trim()) return setError(t("botsPage.needName"));
        create.mutate(
          { name: name.trim(), job: job.trim() || undefined, zone_id: zone || undefined },
          { onSuccess: done, onError: failed },
        );
      }}
    >
      <fieldset>
        <legend className="mb-2 text-sm font-[var(--weight-medium)] text-ink">{t("botsPage.startFrom")}</legend>
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {sources.map((x) => (
            <label
              key={x.id}
              className={cn(
                "flex cursor-pointer gap-2.5 rounded-sm border px-3 py-2.5 transition-colors duration-[var(--dur-1)]",
                source === x.id
                  ? "border-ink bg-surface-raised"
                  : "border-rule hover:border-rule-strong hover:bg-surface-raised",
              )}
            >
              <input
                type="radio"
                name="new-bot-source"
                value={x.id}
                checked={source === x.id}
                onChange={() => {
                  setSource(x.id);
                  setZoneId("");
                  setError(null);
                }}
                className="mt-1 accent-[var(--color-ink)]"
              />
              <span className="min-w-0">
                <span className="block text-sm font-[var(--weight-medium)] text-ink">{x.name}</span>
                <span className="block text-xs text-ink-faint">{x.job}</span>
              </span>
            </label>
          ))}
        </div>
      </fieldset>
      {tmpl ? (
        <p className="text-sm text-ink-soft">{t("botsPage.includes", { skill: tmpl.skill, routine: tmpl.routine })}</p>
      ) : (
        <div className="grid gap-4 sm:grid-cols-[minmax(0,14rem)_1fr]">
          <Field id="new-bot-name" label={t("botsPage.name")} error={error}>
            <input
              id="new-bot-name"
              value={name}
              maxLength={40}
              autoFocus
              aria-invalid={!!error || undefined}
              aria-describedby={error ? "new-bot-name-error" : undefined}
              onChange={(e) => {
                setName(e.target.value);
                setError(null);
              }}
              className={inputCls}
            />
          </Field>
          <Field id="new-bot-job" label={t("botsPage.job")} hint={t("botsPage.jobHint")}>
            <input
              id="new-bot-job"
              value={job}
              maxLength={200}
              aria-describedby="new-bot-job-hint"
              onChange={(e) => setJob(e.target.value)}
              className={inputCls}
            />
          </Field>
        </div>
      )}
      {zoneList.length > 0 && (
        <Field id="new-bot-zone" label={t("botsPage.zone")} hint={t("botsPage.zoneHint")} className="max-w-sm">
          <NativeSelect id="new-bot-zone" value={zone} onChange={setZoneId} describedBy="new-bot-zone-hint">
            {zoneList.map((z) => (
              <option key={z.id} value={z.id}>
                {t("botsPage.zoneOption", { name: z.name, size: t(`computers.sizes.${z.size}`).toLowerCase() })}
              </option>
            ))}
          </NativeSelect>
        </Field>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" size="sm" onClick={onDone}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" size="sm" busy={busy}>
          {tmpl ? t("botsPage.createFrom", { name: tmpl.name }) : t("botsPage.create")}
        </Btn>
      </div>
    </form>
  );
}

export function BotsPage() {
  const { isOwner } = useSession();
  const bots = useBots();
  const [adding, setAdding] = useState(false);
  const list = [...(bots.data ?? [])].sort(
    (a, b) =>
      Number(a.status === "archived") - Number(b.status === "archived") ||
      Number(b.is_primary) - Number(a.is_primary) ||
      a.name.localeCompare(b.name),
  );
  return (
    <Page
      title={t("botsPage.title")}
      wide
      actions={
        isOwner && !adding ? (
          <Btn size="sm" onClick={() => setAdding(true)}>
            <Plus aria-hidden className="size-3.5" />
            {t("botsPage.new")}
          </Btn>
        ) : undefined
      }
    >
      {adding && <NewBotForm onDone={() => setAdding(false)} />}
      {bots.isPending && (
        <div className="space-y-2" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-12 bg-surface-sunk" />
          ))}
        </div>
      )}
      {bots.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("botsPage.loadFailed")}>
          {errorText(bots.error)}
        </Notice>
      )}
      {bots.isSuccess && list.length === 0 && (
        <Notice icon={BotIcon} title={t("botsPage.empty.title")}>
          {isOwner ? t("botsPage.empty.owner") : t("botsPage.empty.member")}
        </Notice>
      )}
      {list.length > 0 && (
        <div aria-hidden className="hidden grid-cols-[auto_1fr_10rem_9rem] gap-x-3 px-2 pb-1.5 sm:grid">
          <span className="label-caps col-span-2">{t("botsPage.col.name")}</span>
          <span className="label-caps">{t("botsPage.col.zone")}</span>
          <span className="label-caps">{t("botsPage.col.status")}</span>
        </div>
      )}
      {list.length > 0 && (
        <ul className="border-t border-rule-strong" aria-label={t("botsPage.list")}>
          {list.map((b) => (
            <li key={b.id} className="border-b border-rule">
              <Link
                to="/bots/$botId"
                params={{ botId: b.id }}
                className={cn(
                  "grid grid-cols-[auto_1fr] items-start gap-x-3 gap-y-1 px-1 py-2.5 hover:bg-surface-sunk sm:grid-cols-[auto_1fr_10rem_9rem] sm:items-center sm:px-2",
                  b.status === "archived" && "opacity-75",
                )}
              >
                <BotMark name={b.name} size="sm" />
                <span className="min-w-0">
                  <span className="flex items-center gap-1.5">
                    <span className="truncate text-sm font-[var(--weight-medium)] text-ink">{b.name}</span>
                    {b.is_primary && (
                      <span className="inline-flex shrink-0 items-center gap-1 text-xs text-ink-faint">
                        <Sparkles aria-hidden className="size-3" />
                        {t("bots.primary")}
                      </span>
                    )}
                  </span>
                  <span className={cn("block truncate text-xs", b.job ? "text-ink-soft" : "text-ink-faint")}>
                    {b.job || t("botsPage.noJob")}
                  </span>
                </span>
                <span className="col-start-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 sm:contents">
                  <span className="truncate text-sm text-ink-soft">{b.zone_name || t("botsPage.noZone")}</span>
                  <BotStatus bot={b} />
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Page>
  );
}
