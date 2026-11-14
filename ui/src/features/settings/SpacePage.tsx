import { useState } from "react";
import { CircleAlert, History } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  usePutSharedMemory,
  useSession,
  useSetSetting,
  useSettingHistory,
  useSettings,
  useSharedMemory,
  useUpdateSpace,
} from "@/api/hooks";
import type { SchemaSettingView } from "@/api/schema";
import { Notice, Section } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { MemoryEditor } from "@/features/bots/MemoryEditor";
import { t } from "@/lib/copy";
import { formatClock, formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";
import { TimeZoneInput } from "./TimeZoneInput";

function SpaceForm() {
  const { space } = useSession();
  const update = useUpdateSpace();
  const [name, setName] = useState(space.name);
  const [tz, setTz] = useState(space.time_zone);
  const dirty = name !== space.name || tz !== space.time_zone;
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        update.mutate(
          { name: name.trim(), time_zone: tz.trim() },
          {
            onSuccess: () => toast.success(t("space.saved")),
            onError: (err) => toast.error(t("space.saveFailed"), { description: errorText(err) }),
          },
        );
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="space-name" label={t("space.name")}>
          <input
            id="space-name"
            value={name}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field id="space-tz" label={t("space.timeZone")} hint={t("space.timeZoneHint")}>
          <TimeZoneInput id="space-tz" value={tz} onChange={setTz} describedBy="space-tz-hint" />
        </Field>
      </div>
      <div className="flex justify-end">
        <Btn type="submit" kind="primary" size="sm" disabled={!dirty || !name.trim()} busy={update.isPending}>
          {t("common.save")}
        </Btn>
      </div>
    </form>
  );
}

function show(v: unknown): string {
  return typeof v === "string" ? v : JSON.stringify(v);
}

function SettingValue({ s }: { s: SchemaSettingView }) {
  const set = useSetSetting();
  const [text, setText] = useState(show(s.value));
  const id = `setting-${s.key}`;
  const commit = (value: unknown) =>
    set.mutate(
      { key: s.key, value },
      {
        onSuccess: () => toast.success(t("space.settingSaved", { key: s.key })),
        onError: (err) => {
          setText(show(s.value));
          toast.error(t("space.settingFailed"), { description: errorText(err) });
        },
      },
    );
  if (typeof s.value === "boolean")
    return (
      <Switch
        id={id}
        checked={s.value}
        disabled={set.isPending}
        onCheckedChange={(v) => commit(v)}
        aria-label={s.key}
      />
    );
  const numeric = typeof s.value === "number";
  const dirty = text !== show(s.value);
  return (
    <form
      className="flex items-center gap-1.5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!dirty) return;
        if (numeric) {
          const n = Number(text);
          if (text.trim() === "" || !Number.isFinite(n)) return toast.error(t("space.needNumber"));
          commit(n);
        } else commit(text);
      }}
    >
      <label htmlFor={id} className="sr-only">
        {s.key}
      </label>
      <input
        id={id}
        value={text}
        inputMode={numeric ? "decimal" : undefined}
        onChange={(e) => setText(e.target.value)}
        className={cn(inputCls, "h-8 w-full font-mono text-sm sm:w-36")}
      />
      <Btn type="submit" size="sm" disabled={!dirty} busy={set.isPending}>
        {t("common.save")}
      </Btn>
    </form>
  );
}

function HistorySheet({ settingKey, onClose }: { settingKey: string | null; onClose: () => void }) {
  const h = useSettingHistory(settingKey);
  return (
    <Sheet open={!!settingKey} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-[min(100vw,26rem)] gap-0 border-rule bg-surface p-0 sm:max-w-md">
        <div className="border-b border-rule px-4 py-3 pr-10">
          <SheetTitle className="serif-title text-lg font-[var(--weight-medium)] text-ink">
            {t("space.historyTitle")}
          </SheetTitle>
          <SheetDescription className="font-mono text-xs text-ink-faint">{settingKey}</SheetDescription>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
          {h.isPending && (
            <p className="text-sm text-ink-faint" role="status">
              {t("common.loading")}
            </p>
          )}
          {h.isError && (
            <Notice icon={CircleAlert} tone="danger" title={t("space.historyFailed")}>
              {errorText(h.error)}
            </Notice>
          )}
          {h.isSuccess && h.data.length === 0 && <p className="text-sm text-ink-faint">{t("space.noHistory")}</p>}
          {h.isSuccess && h.data.length > 0 && (
            <ol className="border-t border-rule">
              {h.data.map((r) => (
                <li key={r.revision} className="border-b border-rule py-2">
                  <div className="flex items-baseline gap-2">
                    <span className="figures text-ink">{t("space.revision", { rev: r.revision })}</span>
                    <span className="figures ml-auto text-ink-faint">
                      {formatDay(r.at)} {formatClock(r.at)}
                    </span>
                  </div>
                  <p className="mt-0.5 font-mono text-xs break-all text-ink-soft">
                    {r.old_value ?? t("space.defaultValue")} → {r.new_value}
                  </p>
                </li>
              ))}
            </ol>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function SettingsList() {
  const settings = useSettings();
  const [history, setHistory] = useState<string | null>(null);
  if (settings.isPending)
    return (
      <p className="text-sm text-ink-faint" role="status">
        {t("common.loading")}
      </p>
    );
  if (settings.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("space.settingsFailed")}>
        {errorText(settings.error)}
      </Notice>
    );
  return (
    <>
      <ul className="border-t border-rule">
        {settings.data.map((s) => (
          <li
            key={s.key}
            className="grid gap-x-4 gap-y-2 border-b border-rule py-3 sm:grid-cols-[1fr_auto] sm:items-center"
          >
            <div className="min-w-0">
              <p className="font-mono text-sm break-all text-ink">{s.key}</p>
              <p className="text-xs text-ink-faint">{s.doc}</p>
              <p className="figures mt-0.5 text-ink-faint">
                {s.is_default ? t("space.isDefault") : t("space.revision", { rev: s.revision })}
              </p>
            </div>
            <div className="flex items-center gap-1.5">
              <SettingValue key={`${s.key}-${s.revision}`} s={s} />
              <Btn
                kind="quiet"
                size="sm"
                aria-label={t("space.history", { key: s.key })}
                onClick={() => setHistory(s.key)}
              >
                <History aria-hidden className="size-3.5" />
              </Btn>
            </div>
          </li>
        ))}
      </ul>
      <HistorySheet settingKey={history} onClose={() => setHistory(null)} />
    </>
  );
}

export function SpacePage() {
  const memory = useSharedMemory();
  const put = usePutSharedMemory();
  return (
    <div className="max-w-3xl">
      <Section id="space" title={t("space.title")}>
        <SpaceForm />
      </Section>
      <Section id="shared-memory" title={t("space.memory")}>
        <p className="mb-2 text-sm text-ink-soft">{t("space.memoryBody")}</p>
        <MemoryEditor
          id="shared-memory-text"
          label={t("space.memory")}
          query={memory}
          onSave={(c) => put.mutateAsync(c)}
          saving={put.isPending}
        />
      </Section>
      <Section id="settings" title={t("space.settings")}>
        <SettingsList />
      </Section>
    </div>
  );
}
