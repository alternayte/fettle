import { useState } from "react";
import { CircleAlert } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useSetSetting, useSettings } from "@/api/hooks";
import type { SchemaSettingView } from "@/api/schema";
import { Notice, Section } from "@/app/Page";
import { Btn, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

/** A budget is one budget.* setting. scale converts the stored value to the unit on screen. */
type Budget = { key: string; scale?: number; decimals?: boolean; fallback: number };

// The keys and defaults come from internal/settings/settings.go.
const WORK: Budget[] = [
  { key: "envelope_usd", decimals: true, fallback: 2 },
  { key: "envelope_tokens", fallback: 2_000_000 },
  { key: "envelope_wall_ms", scale: 60_000, fallback: 3_600_000 },
  { key: "hops", fallback: 4 },
  { key: "max_subagents", fallback: 5 },
];
const SPEND: Budget[] = [
  { key: "bot_day_usd", decimals: true, fallback: 10 },
  { key: "space_month_usd", decimals: true, fallback: 100 },
];

function shown(b: Budget, stored: number): string {
  const v = stored / (b.scale ?? 1);
  return b.decimals ? String(Math.round(v * 100) / 100) : String(Math.round(v));
}

function BudgetRow({ b, s }: { b: Budget; s: SchemaSettingView | undefined }) {
  const set = useSetSetting();
  const stored = typeof s?.value === "number" ? s.value : b.fallback;
  const [text, setText] = useState(shown(b, stored));
  const [error, setError] = useState<string | null>(null);
  const id = `budget-${b.key}`;
  const label = t(`budgets.keys.${b.key}.label`);
  const unit = t(`budgets.keys.${b.key}.unit`);
  const dirty = text.trim() !== shown(b, stored);
  return (
    <li className="grid gap-x-6 gap-y-2 border-b border-rule py-3 sm:grid-cols-[1fr_auto] sm:items-start">
      <div className="min-w-0">
        <label htmlFor={id} className="block text-sm font-[var(--weight-medium)] text-ink">
          {label}
        </label>
        <p id={`${id}-help`} className="text-xs text-ink-faint">
          {t(`budgets.keys.${b.key}.help`)}
        </p>
        <p className="figures mt-0.5 text-ink-faint">
          {s && !s.is_default
            ? t("budgets.changed")
            : t("budgets.default", { value: `${shown(b, b.fallback)} ${unit}` })}
        </p>
      </div>
      <form
        className="flex flex-col gap-1"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (!dirty) return;
          const n = Number(text.trim());
          if (text.trim() === "" || !Number.isFinite(n) || n < 0) return setError(t("budgets.needNumber"));
          const value = b.decimals ? Math.round(n * (b.scale ?? 1) * 100) / 100 : Math.round(n * (b.scale ?? 1));
          set.mutate(
            { key: `budget.${b.key}`, value },
            {
              onSuccess: () => toast.success(t("budgets.saved", { label })),
              onError: (err) => toast.error(t("budgets.failed"), { description: errorText(err) }),
            },
          );
        }}
      >
        <div className="flex items-center gap-1.5">
          <div className="relative w-full sm:w-40">
            <input
              id={id}
              value={text}
              inputMode={b.decimals ? "decimal" : "numeric"}
              aria-invalid={!!error || undefined}
              aria-describedby={cn(`${id}-help`, `${id}-unit`, error && `${id}-error`)}
              onChange={(e) => {
                setText(e.target.value);
                setError(null);
              }}
              className={cn(inputCls, "h-8 pr-16 text-right font-mono text-sm")}
            />
            <span
              id={`${id}-unit`}
              className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-xs text-ink-faint"
            >
              {unit}
            </span>
          </div>
          <Btn type="submit" size="sm" disabled={!dirty} busy={set.isPending}>
            {t("common.save")}
          </Btn>
        </div>
        {error && (
          <p id={`${id}-error`} role="alert" className="text-xs text-danger">
            {error}
          </p>
        )}
      </form>
    </li>
  );
}

export function BudgetsPage() {
  const settings = useSettings();
  const byKey = new Map((settings.data ?? []).map((s) => [s.key, s]));
  const rows = (list: Budget[]) => (
    <ul className="border-t border-rule">
      {list.map((b) => {
        const s = byKey.get(`budget.${b.key}`);
        return <BudgetRow key={`${b.key}-${s?.revision ?? 0}`} b={b} s={s} />;
      })}
    </ul>
  );
  return (
    <div className="max-w-3xl">
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("budgets.intro")}</p>
      {settings.isPending && (
        <p className="text-sm text-ink-faint" role="status">
          {t("common.loading")}
        </p>
      )}
      {settings.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("budgets.loadFailed")}>
          {errorText(settings.error)}
        </Notice>
      )}
      {settings.isSuccess && (
        <>
          <Section id="budget-work" title={t("budgets.work")}>
            {rows(WORK)}
          </Section>
          <Section id="budget-spend" title={t("budgets.spend")}>
            {rows(SPEND)}
          </Section>
        </>
      )}
    </div>
  );
}
