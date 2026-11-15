import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CircleAlert, Download, History, Upload } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  keys,
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

function ExportSpace() {
  const { space } = useSession();
  return (
    // A native form, so the browser downloads the archive as it streams.
    <form method="post" action={`/api/v1/spaces/${space.id}/export`} className="space-y-4">
      <p className="text-sm text-ink-soft">{t("space.export.body")}</p>
      <Field
        id="export-passphrase"
        label={t("space.export.passphrase")}
        hint={t("space.export.passphraseHint")}
        className="max-w-sm"
      >
        <input
          id="export-passphrase"
          name="passphrase"
          type="password"
          autoComplete="new-password"
          aria-describedby="export-passphrase-hint"
          className={inputCls}
        />
      </Field>
      <Btn type="submit">
        <Download aria-hidden className="size-3.5" />
        {t("space.export.submit")}
      </Btn>
    </form>
  );
}

function ImportSpace() {
  const qc = useQueryClient();
  const { chooseSpace } = useSession();
  const [file, setFile] = useState<File | null>(null);
  const [pass, setPass] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      className="space-y-4"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!file) return setError(t("space.import.needFile"));
        setBusy(true);
        const body = new FormData();
        if (pass) body.append("passphrase", pass);
        body.append("file", file);
        try {
          const r = await fetch("/api/v1/space-import", { method: "POST", credentials: "same-origin", body });
          const data = (await r.json().catch(() => ({}))) as { space_id?: string; detail?: string };
          if (!r.ok || !data.space_id) throw new Error(data.detail ?? String(r.status));
          await qc.invalidateQueries({ queryKey: keys.me });
          const id = data.space_id;
          toast.success(t("space.import.done", { name: file.name }), {
            action: { label: t("space.import.open"), onClick: () => chooseSpace(id) },
          });
          setFile(null);
          setPass("");
        } catch (err) {
          toast.error(t("space.import.failed"), { description: errorText(err) });
        } finally {
          setBusy(false);
        }
      }}
    >
      <p className="text-sm text-ink-soft">{t("space.import.body")}</p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="import-file" label={t("space.import.file")} hint={t("space.import.fileHint")} error={error}>
          <input
            id="import-file"
            type="file"
            accept=".gz,.tgz,application/gzip"
            aria-describedby="import-file-hint"
            onChange={(e) => {
              setFile(e.target.files?.[0] ?? null);
              setError(null);
            }}
            className={cn(inputCls, "py-1.5")}
          />
        </Field>
        <Field id="import-passphrase" label={t("space.import.passphrase")} hint={t("space.import.passphraseHint")}>
          <input
            id="import-passphrase"
            type="password"
            autoComplete="off"
            value={pass}
            aria-describedby="import-passphrase-hint"
            onChange={(e) => setPass(e.target.value)}
            className={inputCls}
          />
        </Field>
      </div>
      <Btn type="submit" busy={busy}>
        <Upload aria-hidden className="size-3.5" />
        {t("space.import.submit")}
      </Btn>
    </form>
  );
}

export function SpacePage() {
  const { me } = useSession();
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
      <Section id="export" title={t("space.export.title")}>
        <ExportSpace />
      </Section>
      {me.is_admin && (
        <Section id="import" title={t("space.import.title")}>
          <ImportSpace />
        </Section>
      )}
    </div>
  );
}
