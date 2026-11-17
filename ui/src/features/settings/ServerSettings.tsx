import { useState } from "react";
import { CircleAlert, Lock } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useInstanceSettings, useSetInstanceSetting } from "@/api/hooks";
import type { SchemaInstanceSettingView } from "@/api/schema";
import { Notice } from "@/app/Page";
import { Btn, Field, inputCls, textareaCls } from "@/components/form";
import { Switch } from "@/components/ui/switch";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

type View = SchemaInstanceSettingView;

const text = (v: unknown) => (typeof v === "string" ? v : "");

function Locked({ s }: { s: View }) {
  return (
    <p className="flex items-center gap-1.5 text-xs text-ink-faint">
      <Lock aria-hidden className="size-3" />
      {t("computers.server.locked", { env: s.env })}
    </p>
  );
}

function useSave(key: string) {
  const set = useSetInstanceSetting();
  const save = (value: unknown, done?: string) =>
    set.mutate(
      { key, value },
      {
        onSuccess: () => toast.success(done ?? t("computers.server.saved")),
        onError: (err) => toast.error(t("computers.saveFailed"), { description: errorText(err) }),
      },
    );
  return { save, busy: set.isPending };
}

/** TextSetting edits one text setting; the default shows as the placeholder. */
function TextSetting({ s, label, hint, multiline }: { s: View; label: string; hint: string; multiline?: boolean }) {
  const stored = s.is_default ? "" : text(s.value);
  const [value, setValue] = useState(stored);
  const { save, busy } = useSave(s.key);
  const id = `server-${s.key.replace(/\./g, "-")}`;
  const placeholder = text(s.default);
  const props = {
    id,
    value: s.locked ? text(s.value) : value,
    readOnly: s.locked,
    spellCheck: false,
    placeholder,
    "aria-describedby": `${id}-hint`,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setValue(e.target.value),
  };
  return (
    <div className="space-y-2">
      <Field id={id} label={label} hint={hint}>
        {multiline ? (
          <textarea {...props} rows={5} className={cn(textareaCls, "font-mono text-xs")} />
        ) : (
          <input {...props} className={cn(inputCls, "font-mono text-sm")} />
        )}
      </Field>
      {s.locked ? (
        <Locked s={s} />
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          <Btn
            size="sm"
            kind="primary"
            busy={busy}
            disabled={value.trim() === stored.trim()}
            onClick={() => save(value)}
          >
            {t("common.save")}
          </Btn>
          {!s.is_default && (
            <Btn
              size="sm"
              kind="quiet"
              disabled={busy}
              onClick={() => {
                setValue("");
                save("", t("computers.server.reset"));
              }}
            >
              {t("computers.server.resetDefault")}
            </Btn>
          )}
        </div>
      )}
    </div>
  );
}

/** SecretSetting is write-only: it shows whether a value is set, never the value. */
function SecretSetting({ s, label }: { s: View; label: string }) {
  const [value, setValue] = useState("");
  const { save, busy } = useSave(s.key);
  const id = `server-${s.key.replace(/\./g, "-")}`;
  return (
    <div className="space-y-2">
      <Field id={id} label={label} hint={s.set ? t("computers.server.secretSet") : t("computers.server.secretUnset")}>
        <input
          id={id}
          type="password"
          autoComplete="new-password"
          value={value}
          readOnly={s.locked}
          aria-describedby={`${id}-hint`}
          onChange={(e) => setValue(e.target.value)}
          className={cn(inputCls, "font-mono text-sm")}
        />
      </Field>
      {s.locked ? (
        <Locked s={s} />
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          <Btn
            size="sm"
            kind="primary"
            busy={busy}
            disabled={value === ""}
            onClick={() => {
              save(value);
              setValue("");
            }}
          >
            {t("common.save")}
          </Btn>
          {s.set && (
            <Btn size="sm" kind="quiet" disabled={busy} onClick={() => save("", t("computers.server.cleared"))}>
              {t("computers.server.clear")}
            </Btn>
          )}
        </div>
      )}
    </div>
  );
}

function SwitchSetting({ s, label, hint }: { s: View; label: string; hint: string }) {
  const { save, busy } = useSave(s.key);
  const on = s.value === true || (s.value as unknown) === "true";
  const id = `server-${s.key.replace(/\./g, "-")}`;
  return (
    <div className="flex items-start gap-3 rounded-sm border border-rule bg-surface px-3 py-3">
      <Switch id={id} checked={on} disabled={busy || s.locked} onCheckedChange={(v) => save(v)} className="mt-0.5" />
      <div className="min-w-0">
        <label htmlFor={id} className="text-sm font-[var(--weight-medium)] text-ink">
          {label}
        </label>
        <p className="text-xs text-ink-faint">{hint}</p>
      </div>
    </div>
  );
}

/** ServerSettings is the "This server" section of Settings, Computers, for instance admins. */
export function ServerSettings() {
  const q = useInstanceSettings(true);
  if (q.isPending)
    return (
      <p role="status" className="text-sm text-ink-faint">
        {t("common.loading")}
      </p>
    );
  if (q.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("computers.server.loadFailed")}>
        {errorText(q.error)}
      </Notice>
    );
  const by = Object.fromEntries(q.data.map((s) => [s.key, s]));
  const image = by["computers.image"];
  const ca = by["computers.extra_ca"];
  const proxy = by["computers.proxy"];
  const password = by["computers.proxy_password"];
  const host = by["computers.use_host_settings"];
  return (
    <div className="max-w-[var(--measure)] space-y-6">
      <p className="text-sm text-ink-soft">{t("computers.server.intro")}</p>
      {image && (
        <TextSetting
          key={`${image.key}-${String(image.value)}`}
          s={image}
          label={t("computers.server.image")}
          hint={t("computers.server.imageHint", { image: text(image.default) })}
        />
      )}
      {host && (
        <SwitchSetting s={host} label={t("computers.server.useHost")} hint={t("computers.server.useHostHint")} />
      )}
      {ca && (
        <TextSetting
          key={`${ca.key}-${String(ca.value)}`}
          s={ca}
          label={t("computers.server.extraCA")}
          hint={t("computers.server.extraCAHint")}
          multiline
        />
      )}
      {proxy && (
        <TextSetting
          key={`${proxy.key}-${String(proxy.value)}`}
          s={proxy}
          label={t("computers.server.proxy")}
          hint={t("computers.server.proxyHint")}
        />
      )}
      {password && <SecretSetting s={password} label={t("computers.server.proxyPassword")} />}
    </div>
  );
}
