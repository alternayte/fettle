import { useState } from "react";
import { Check, Eye, EyeOff, Loader, X } from "lucide-react";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

export type CheckState = "idle" | "running" | "pass" | "fail";

export const inputCls =
  "h-9 w-full min-w-0 rounded-sm border border-rule-strong bg-surface-raised px-2.5 text-base text-ink outline-none placeholder:text-ink-faint focus-visible:border-ink focus-visible:outline-none disabled:opacity-60 aria-invalid:border-danger";

export const textareaCls =
  "block w-full min-w-0 rounded-sm border border-rule-strong bg-surface-raised px-2.5 py-2 text-base text-ink outline-none placeholder:text-ink-faint focus-visible:border-ink focus-visible:outline-none disabled:opacity-60 aria-invalid:border-danger";

export function StateIcon({ s }: { s: CheckState }) {
  if (s === "pass") return <Check aria-label={t("setup.check.pass")} className="size-3.5 shrink-0 text-ok" />;
  if (s === "fail") return <X aria-label={t("setup.check.fail")} className="size-3.5 shrink-0 text-danger" />;
  if (s === "running")
    return <Loader aria-label={t("setup.check.running")} className="size-3.5 shrink-0 animate-spin text-info" />;
  return (
    <span
      aria-label={t("setup.check.idle")}
      className="inline-block size-3.5 shrink-0 rounded-full border border-rule-strong"
    />
  );
}

export function Field({
  id,
  label,
  children,
  hint,
  error,
  className,
}: {
  id: string;
  label: string;
  children: React.ReactNode;
  hint?: React.ReactNode;
  error?: string | null;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0 space-y-1", className)}>
      <label htmlFor={id} className="block text-sm font-[var(--weight-medium)] text-ink">
        {label}
      </label>
      {children}
      {error ? (
        <p id={`${id}-error`} className="flex items-center gap-1 text-xs text-danger" role="alert">
          <X aria-hidden className="size-3" />
          {error}
        </p>
      ) : (
        hint && (
          <p id={`${id}-hint`} className="text-xs text-ink-faint">
            {hint}
          </p>
        )
      )}
    </div>
  );
}

/** SecretInput is a password field with an eye toggle that shows the typed value before save. */
export function SecretInput({
  id,
  value,
  onChange,
  placeholder,
  disabled,
  describedBy,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  disabled?: boolean;
  describedBy?: string;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="relative">
      <input
        id={id}
        type={show ? "text" : "password"}
        autoComplete="off"
        spellCheck={false}
        value={value}
        disabled={disabled}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={cn(inputCls, "pr-9 font-mono text-sm")}
      />
      <button
        type="button"
        onClick={() => setShow((v) => !v)}
        aria-label={show ? t("vault.hide") : t("vault.show")}
        aria-pressed={show}
        aria-controls={id}
        className="absolute top-1/2 right-1 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-xs text-ink-faint hover:text-ink"
      >
        {show ? <EyeOff className="size-4" aria-hidden /> : <Eye className="size-4" aria-hidden />}
      </button>
    </div>
  );
}

type BtnKind = "primary" | "act" | "plain" | "quiet" | "danger";

/** Btn uses the button shapes of the approved chat and setup screens. */
export function Btn({
  kind = "plain",
  size = "md",
  busy,
  className,
  children,
  disabled,
  type = "button",
  ...rest
}: React.ComponentProps<"button"> & { kind?: BtnKind; size?: "sm" | "md"; busy?: boolean }) {
  return (
    <button
      type={type}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={cn(
        "inline-flex shrink-0 items-center justify-center gap-1.5 rounded-sm font-[var(--weight-medium)] whitespace-nowrap transition-colors duration-[var(--dur-1)]",
        "disabled:pointer-events-none disabled:opacity-50",
        size === "md" ? "h-9 px-3.5 text-sm" : "h-7 px-2.5 text-sm",
        kind === "primary" && "bg-ink text-surface hover:opacity-90",
        kind === "act" && "bg-glaze text-glaze-ink hover:brightness-105",
        kind === "plain" && "border border-rule-strong bg-surface-raised text-ink hover:bg-surface-sunk",
        kind === "quiet" && "text-ink-soft hover:bg-surface-sunk hover:text-ink",
        kind === "danger" && "text-danger hover:bg-danger-wash",
        className,
      )}
      {...rest}
    >
      {busy && <Loader aria-hidden className="size-3.5 animate-spin" />}
      {children}
    </button>
  );
}

export interface CheckRow {
  key: string;
  state: CheckState;
  detail?: string;
  ms?: number;
}

/** CheckList shows the four model checks with an icon and a word for each state. */
export function CheckList({ checks, label }: { checks: CheckRow[]; label: string }) {
  return (
    <ul className="rounded-sm border border-rule bg-surface-raised" aria-label={label}>
      {checks.map((c) => (
        <li key={c.key} className="border-b border-rule/70 px-3 py-2 last:border-b-0">
          <div className="flex items-center gap-2.5">
            <StateIcon s={c.state} />
            <span className="text-sm text-ink">{t(`models.check.${c.key}`)}</span>
            <span className="figures ml-auto shrink-0 text-ink-faint">
              {c.state === "pass" || c.state === "fail" ? t(`setup.check.${c.state}`) : null}
              {c.ms !== undefined && c.state !== "running" && c.state !== "idle" ? ` · ${c.ms} ms` : null}
            </span>
          </div>
          {c.detail && <p className="mt-0.5 pl-6 text-xs break-words text-ink-faint">{c.detail}</p>}
        </li>
      ))}
    </ul>
  );
}

export const CHECK_KEYS = ["plain", "tools", "schema", "vision"] as const;

export function Bytes({ used, limit }: { used: number; limit: number }) {
  const over = used > limit;
  return (
    <span className={cn("figures", over ? "text-danger" : "text-ink-faint")} aria-live="polite">
      {t("memory.bytes", { used, limit })}
      {over && ` · ${t("memory.over")}`}
    </span>
  );
}
