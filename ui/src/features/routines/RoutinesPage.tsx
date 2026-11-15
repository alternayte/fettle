import { useState } from "react";
import { AlarmClock, Play, Plus, Trash2, Webhook } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useBots,
  useDeleteRoutine,
  useRoutineRuns,
  useRoutines,
  useRunRoutine,
  useSaveRoutine,
  useSession,
} from "@/api/hooks";
import type { SchemaRoutineBody, SchemaRoutineView } from "@/api/schema";
import { Notice, Page, Section } from "@/app/Page";
import { Btn, Field, inputCls, textareaCls } from "@/components/form";
import { Switch } from "@/components/ui/switch";
import { NativeSelect } from "@/features/settings/ProviderForm";
import { TimeZoneInput } from "@/features/settings/TimeZoneInput";
import { t } from "@/lib/copy";
import { formatClock, formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";

type Trigger = "cron" | "webhook";
type Notify = "always" | "on_failure" | "never";

function RoutineForm({
  routine,
  onDone,
  onCancel,
}: {
  routine?: SchemaRoutineView;
  onDone: (secret?: string) => void;
  onCancel: () => void;
}) {
  const { space } = useSession();
  const bots = useBots();
  const save = useSaveRoutine();
  const [botID, setBotID] = useState(routine?.bot_id ?? "");
  const [name, setName] = useState(routine?.name ?? "");
  const [prompt, setPrompt] = useState(routine?.prompt ?? "");
  const [trigger, setTrigger] = useState<Trigger>((routine?.trigger as Trigger) ?? "cron");
  const [cron, setCron] = useState(routine?.cron || "0 9 * * 1-5");
  const [tz, setTz] = useState(routine?.tz || space.time_zone || "UTC");
  const [notify, setNotify] = useState<Notify>((routine?.notify as Notify) ?? "on_failure");
  const [error, setError] = useState<string | null>(null);
  const list = bots.data ?? [];
  const bot = botID || list[0]?.id || "";
  const p = routine ? `routine-${routine.id}` : "routine-new";
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        if (!name.trim() || !prompt.trim()) return setError(t("routines.needName"));
        if (trigger === "cron" && !cron.trim()) return setError(t("routines.needCron"));
        const body: SchemaRoutineBody = {
          bot_id: bot,
          name: name.trim(),
          prompt: prompt.trim(),
          trigger,
          cron: trigger === "cron" ? cron.trim() : undefined,
          tz: trigger === "cron" ? tz : undefined,
          notify,
          enabled: routine?.enabled ?? true,
        };
        save.mutate(
          { id: routine?.id, body },
          { onSuccess: (r) => onDone(r.secret), onError: (err) => setError(errorText(err)) },
        );
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={`${p}-name`} label={t("routines.name")}>
          <input
            id={`${p}-name`}
            value={name}
            maxLength={120}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field id={`${p}-bot`} label={t("routines.bot")}>
          <NativeSelect id={`${p}-bot`} value={bot} onChange={setBotID}>
            {list.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
      <Field id={`${p}-prompt`} label={t("routines.prompt")} hint={t("routines.promptHint")}>
        <textarea
          id={`${p}-prompt`}
          rows={3}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          className={textareaCls}
        />
      </Field>
      {!routine && (
        <fieldset className="space-y-2">
          <legend className="text-sm font-[var(--weight-medium)] text-ink">{t("routines.trigger")}</legend>
          <div className="flex flex-wrap gap-4">
            {(["cron", "webhook"] as const).map((x) => (
              <label key={x} className="flex items-center gap-2 text-sm text-ink">
                <input
                  type="radio"
                  name={`${p}-trigger`}
                  checked={trigger === x}
                  onChange={() => setTrigger(x)}
                  className="accent-[var(--accent)]"
                />
                {t(`routines.triggers.${x}`)}
              </label>
            ))}
          </div>
        </fieldset>
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        {trigger === "cron" && (
          <>
            <Field id={`${p}-cron`} label={t("routines.cron")} hint={t("routines.cronHint")}>
              <input
                id={`${p}-cron`}
                value={cron}
                spellCheck={false}
                aria-describedby={`${p}-cron-hint`}
                onChange={(e) => setCron(e.target.value)}
                className={cn(inputCls, "font-mono text-sm")}
              />
            </Field>
            <Field id={`${p}-tz`} label={t("routines.tz")}>
              <TimeZoneInput id={`${p}-tz`} value={tz} onChange={setTz} />
            </Field>
          </>
        )}
        <Field id={`${p}-notify`} label={t("routines.notify")}>
          <NativeSelect id={`${p}-notify`} value={notify} onChange={(v) => setNotify(v as Notify)}>
            {(["always", "on_failure", "never"] as const).map((x) => (
              <option key={x} value={x}>
                {t(`routines.notifies.${x}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onCancel}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={save.isPending}>
          {t("common.save")}
        </Btn>
      </div>
    </form>
  );
}

function RunHistory({ id }: { id: string }) {
  const runs = useRoutineRuns(id);
  const list = runs.data ?? [];
  if (list.length === 0) return <p className="py-2 text-sm text-ink-faint">{t("routines.noRuns")}</p>;
  return (
    <ol aria-label={t("routines.history")} className="divide-y divide-rule">
      {list.map((r) => (
        <li
          key={`${r.due_at}-${r.coalesced}`}
          className="grid grid-cols-[7rem_1fr_auto] items-center gap-3 py-1.5 text-sm"
        >
          <span className="figures text-ink-soft">
            {formatDay(r.due_at)} {formatClock(r.due_at)}
          </span>
          <span className="min-w-0 truncate text-ink-soft">
            {r.coalesced ? t("routines.coalesced") : t(`routines.source.${r.source}`)}
            {r.missed && <span className="ml-2 text-warn">· {t("routines.missed")}</span>}
          </span>
          {r.run_id ? (
            <a href={`/runs/${r.run_id}`} className="figures text-ink underline underline-offset-2">
              {r.state}
            </a>
          ) : (
            <span />
          )}
        </li>
      ))}
    </ol>
  );
}

function RoutineRow({ r, botName, canEdit }: { r: SchemaRoutineView; botName: string; canEdit: boolean }) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const save = useSaveRoutine();
  const del = useDeleteRoutine();
  const run = useRunRoutine();
  const Icon = r.trigger === "webhook" ? Webhook : AlarmClock;
  return (
    <li className="border-b border-rule py-3">
      <div className="grid grid-cols-[auto_1fr_auto] items-center gap-x-3 gap-y-1">
        <Icon aria-hidden className="size-4 text-ink-faint" />
        <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="min-w-0 text-left">
          <span className="block truncate text-sm font-[var(--weight-medium)] text-ink">{r.name}</span>
          <span className="figures block truncate text-ink-faint">
            {botName} ·{" "}
            {r.trigger === "cron"
              ? `${r.cron} · ${r.tz}${
                  r.enabled && r.next_at
                    ? ` · ${t("routines.next", { when: `${formatDay(r.next_at)} ${formatClock(r.next_at)}` })}`
                    : ""
                }`
              : r.webhook_url}
          </span>
        </button>
        <div className="flex items-center gap-2">
          {canEdit ? (
            <label className="flex items-center gap-1.5 text-xs text-ink-soft">
              <Switch
                checked={r.enabled}
                aria-label={r.enabled ? t("routines.enabled") : t("routines.paused")}
                onCheckedChange={(v) =>
                  save.mutate({
                    id: r.id,
                    body: {
                      bot_id: r.bot_id,
                      name: r.name,
                      prompt: r.prompt,
                      trigger: r.trigger,
                      cron: r.cron,
                      tz: r.tz,
                      notify: r.notify,
                      enabled: v,
                    },
                  })
                }
              />
              {r.enabled ? t("routines.enabled") : t("routines.paused")}
            </label>
          ) : (
            <span className="text-xs text-ink-soft">{r.enabled ? t("routines.enabled") : t("routines.paused")}</span>
          )}
        </div>
      </div>
      {open && (
        <div className="mt-2 ml-7 space-y-3">
          <p className="max-w-[var(--measure)] text-sm whitespace-pre-wrap text-ink-soft">{r.prompt}</p>
          {canEdit && !editing && (
            <div className="flex flex-wrap gap-1.5">
              <Btn
                size="sm"
                busy={run.isPending}
                onClick={() =>
                  run.mutate(r.id, {
                    onSuccess: () => toast.success(t("routines.started")),
                    onError: (err) => toast.error(errorText(err)),
                  })
                }
              >
                <Play aria-hidden className="size-3.5" />
                {t("routines.runNow")}
              </Btn>
              <Btn size="sm" kind="quiet" onClick={() => setEditing(true)}>
                {t("common.edit")}
              </Btn>
              <Btn
                size="sm"
                kind="danger"
                busy={del.isPending}
                onClick={() => del.mutate(r.id, { onSuccess: () => toast.success(t("routines.deleted")) })}
              >
                <Trash2 aria-hidden className="size-3.5" />
                {t("routines.delete")}
              </Btn>
            </div>
          )}
          {editing && (
            <div className="rounded-sm border border-rule bg-surface p-4">
              <RoutineForm
                routine={r}
                onCancel={() => setEditing(false)}
                onDone={() => {
                  setEditing(false);
                  toast.success(t("routines.saved"));
                }}
              />
            </div>
          )}
          <div>
            <h3 className="label-caps mb-1">{t("routines.history")}</h3>
            <RunHistory id={r.id} />
          </div>
        </div>
      )}
    </li>
  );
}

export function RoutinesPage() {
  const { isOwner } = useSession();
  const routines = useRoutines();
  const bots = useBots();
  const [adding, setAdding] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const names = Object.fromEntries((bots.data ?? []).map((b) => [b.id, b.name]));
  return (
    <Page title={t("routines.title")} wide>
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("routines.intro")}</p>
      {secret && (
        <div className="mb-6 rounded-sm border border-glaze/50 bg-glaze-wash px-4 py-3">
          <p className="text-sm font-[var(--weight-strong)] text-ink">{t("routines.secretTitle")}</p>
          <p className="mt-1 text-sm text-ink-soft">{t("routines.secretBody")}</p>
          <p className="mt-2 font-mono text-sm break-all text-ink select-all">{secret}</p>
        </div>
      )}
      <Section
        id="routines"
        title={t("routines.title")}
        aside={
          isOwner &&
          !adding && (
            <Btn size="sm" onClick={() => setAdding(true)}>
              <Plus aria-hidden className="size-3.5" />
              {t("routines.new")}
            </Btn>
          )
        }
      >
        {adding && (
          <div className="mb-3 rounded-sm border border-rule bg-surface p-4">
            <RoutineForm
              onCancel={() => setAdding(false)}
              onDone={(s) => {
                setAdding(false);
                if (s) setSecret(s);
                toast.success(t("routines.saved"));
              }}
            />
          </div>
        )}
        {routines.isError ? (
          <Notice title={t("routines.failed")} tone="danger">
            {errorText(routines.error)}
          </Notice>
        ) : (routines.data ?? []).length === 0 && !adding ? (
          <p className="border-y border-rule py-3 text-sm text-ink-faint">{t("routines.none")}</p>
        ) : (
          <ul aria-label={t("routines.title")}>
            {(routines.data ?? []).map((r) => (
              <RoutineRow key={r.id} r={r} botName={names[r.bot_id] ?? ""} canEdit={isOwner} />
            ))}
          </ul>
        )}
        {!isOwner && <p className="mt-2 text-xs text-ink-faint">{t("routines.ownerOnly")}</p>}
      </Section>
    </Page>
  );
}
