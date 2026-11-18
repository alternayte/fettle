import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { errorText } from "@/api/client";
import { useCreateProvider, usePresets, useUpdateProvider } from "@/api/hooks";
import type { SchemaPreset, SchemaProviderBody, SchemaProviderView } from "@/api/schema";
import { Btn, Field, SecretInput, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

type ApiType = NonNullable<SchemaProviderBody["api_type"]>;
type AuthStyle = NonNullable<SchemaProviderBody["auth_style"]>;
const API_TYPES: ApiType[] = ["openai-chat", "openai-responses", "anthropic-messages"];
const AUTH_STYLES: AuthStyle[] = ["bearer", "x_api_key", "header", "none"];

const selectCls = cn(inputCls, "appearance-none pr-8");

export function NativeSelect({
  id,
  value,
  onChange,
  children,
  className,
  describedBy,
  disabled,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  children: React.ReactNode;
  className?: string;
  describedBy?: string;
  disabled?: boolean;
}) {
  return (
    <div className={cn("relative", className)}>
      <select
        id={id}
        value={value}
        disabled={disabled}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
        className={selectCls}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden
        className="pointer-events-none absolute top-1/2 right-2.5 size-3.5 -translate-y-1/2 text-ink-faint"
      />
    </div>
  );
}

/**
 * ProviderForm adds or changes a model provider. A preset fills the base URL, the API type and
 * the auth style. The key field shows the typed key on request; after save only its last four
 * characters come back.
 */
export function ProviderForm({
  provider,
  initialPreset = "openrouter",
  onSaved,
  onCancel,
  submitLabel,
  idPrefix = "prov",
}: {
  provider?: SchemaProviderView;
  initialPreset?: string;
  onSaved: (p: SchemaProviderView) => void;
  onCancel?: () => void;
  submitLabel?: string;
  idPrefix?: string;
}) {
  const presets = usePresets();
  const create = useCreateProvider();
  const update = useUpdateProvider();
  const [preset, setPreset] = useState(provider?.preset || initialPreset);
  const [name, setName] = useState(provider?.name ?? "");
  const [baseURL, setBaseURL] = useState(provider?.base_url ?? "");
  const [apiType, setApiType] = useState<ApiType>((provider?.api_type as ApiType) || "openai-chat");
  const [authStyle, setAuthStyle] = useState<AuthStyle>((provider?.auth_style as AuthStyle) || "bearer");
  const [authHeader, setAuthHeader] = useState(provider?.auth_header ?? "");
  const [key, setKey] = useState("");
  const [more, setMore] = useState(!!provider);
  const [error, setError] = useState<string | null>(null);
  const [filled, setFilled] = useState(!!provider);

  const list = presets.data ?? [];
  const current: SchemaPreset | undefined = list.find((p) => p.id === preset);

  // The first preset list fills the form for a new provider once.
  if (!filled && current) {
    setFilled(true);
    setBaseURL(current.base_url);
    setApiType(current.api_type as ApiType);
    setAuthStyle(current.auth_style as AuthStyle);
    setAuthHeader(current.auth_header ?? "");
  }

  const choosePreset = (id: string) => {
    setPreset(id);
    const p = list.find((x) => x.id === id);
    if (!p) return;
    setBaseURL(p.base_url);
    setApiType(p.api_type as ApiType);
    setAuthStyle(p.auth_style as AuthStyle);
    setAuthHeader(p.auth_header ?? "");
    if (!provider) setName("");
  };

  const needsKey = authStyle !== "none";
  const busy = create.isPending || update.isPending;
  const id = (s: string) => `${idPrefix}-${s}`;

  const submit = () => {
    setError(null);
    if (!baseURL.trim()) return setError(t("models.form.needBaseURL"));
    if (!provider && needsKey && current?.needs_key !== false && !key.trim()) return setError(t("models.form.needKey"));
    const body: SchemaProviderBody = {
      preset,
      name: name.trim() || current?.name || preset,
      base_url: baseURL.trim(),
      api_type: apiType,
      auth_style: authStyle,
      auth_header: authStyle === "header" ? authHeader.trim() : undefined,
      key: key.trim() || undefined,
    };
    const done = {
      onSuccess: (p: SchemaProviderView) => {
        setKey("");
        onSaved(p);
      },
      onError: (err: unknown) => setError(errorText(err)),
    };
    if (provider) update.mutate({ id: provider.id, body }, done);
    else create.mutate(body, done);
  };

  return (
    <form
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="space-y-4"
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={id("preset")} label={t("models.form.preset")} hint={t("models.form.presetHint")}>
          <NativeSelect id={id("preset")} value={preset} onChange={choosePreset} describedBy={id("preset-hint")}>
            {list.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id={id("base")} label={t("models.form.baseURL")}>
          <input
            id={id("base")}
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            spellCheck={false}
            inputMode="url"
            placeholder="https://…/v1"
            className={cn(inputCls, "font-mono text-sm")}
          />
        </Field>
      </div>
      {needsKey && (
        <Field
          id={id("key")}
          label={t("models.form.key", { provider: current?.name ?? t("models.form.provider") })}
          hint={provider?.has_key ? t("models.form.keyKeep", { last4: provider.key_last4 }) : t("models.form.keyHint")}
        >
          <SecretInput
            id={id("key")}
            value={key}
            onChange={setKey}
            describedBy={id("key-hint")}
            placeholder={provider?.has_key ? `•••• ${provider.key_last4}` : undefined}
          />
        </Field>
      )}
      <div>
        <button
          type="button"
          aria-expanded={more}
          aria-controls={id("more")}
          onClick={() => setMore((m) => !m)}
          className="inline-flex items-center gap-1 text-sm text-ink-soft hover:text-ink"
        >
          <ChevronDown aria-hidden className={cn("size-3.5 transition-transform", more && "rotate-180")} />
          {t("models.form.more")}
        </button>
        {more && (
          <div id={id("more")} className="mt-3 grid gap-4 sm:grid-cols-2">
            <Field id={id("name")} label={t("models.form.name")}>
              <input
                id={id("name")}
                value={name}
                maxLength={80}
                placeholder={current?.name}
                onChange={(e) => setName(e.target.value)}
                className={inputCls}
              />
            </Field>
            <Field id={id("api")} label={t("models.form.apiType")}>
              <NativeSelect id={id("api")} value={apiType} onChange={(v) => setApiType(v as ApiType)}>
                {API_TYPES.map((a) => (
                  <option key={a} value={a}>
                    {t(`models.apiType.${a}`)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Field id={id("auth")} label={t("models.form.authStyle")}>
              <NativeSelect id={id("auth")} value={authStyle} onChange={(v) => setAuthStyle(v as AuthStyle)}>
                {AUTH_STYLES.map((a) => (
                  <option key={a} value={a}>
                    {t(`models.authStyle.${a}`)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            {authStyle === "header" && (
              <Field id={id("header")} label={t("models.form.authHeader")}>
                <input
                  id={id("header")}
                  value={authHeader}
                  spellCheck={false}
                  onChange={(e) => setAuthHeader(e.target.value)}
                  className={cn(inputCls, "font-mono text-sm")}
                />
              </Field>
            )}
          </div>
        )}
      </div>
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex items-center justify-end gap-1.5">
        {onCancel && (
          <Btn kind="quiet" onClick={onCancel}>
            {t("common.cancel")}
          </Btn>
        )}
        <Btn type="submit" kind="primary" busy={busy}>
          {submitLabel ?? (provider ? t("common.save") : t("models.form.add"))}
        </Btn>
      </div>
    </form>
  );
}
