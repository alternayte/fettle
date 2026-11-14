import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { CircleAlert, Coins } from "lucide-react";
import { errorText } from "@/api/client";
import { useBots, useUsage, type UsageDimension } from "@/api/hooks";
import { Notice, Section } from "@/app/Page";
import { has, t } from "@/lib/copy";
import { formatDuration, formatTokens, formatUSD } from "@/lib/format";
import { cn } from "@/lib/utils";
import { NativeSelect } from "./ProviderForm";

const DIMS: UsageDimension[] = ["day", "month", "bot", "run", "step", "role", "model"];
const RANGES = [7, 30, 90, 365];

export function UsagePage() {
  const [by, setBy] = useState<UsageDimension>("day");
  const [days, setDays] = useState(30);
  const usage = useUsage(by, days);
  const bots = useBots();
  const botName = Object.fromEntries((bots.data ?? []).map((b) => [b.id, b.name]));
  const rows = usage.data ?? [];
  const total = rows.reduce(
    (a, r) => ({
      calls: a.calls + r.calls,
      tin: a.tin + r.tokens_in,
      tout: a.tout + r.tokens_out,
      sec: a.sec + r.seconds,
      cost: a.cost + r.cost_usd,
    }),
    { calls: 0, tin: 0, tout: 0, sec: 0, cost: 0 },
  );

  const keyCell = (key: string) => {
    if (!key) return <span className="text-ink-faint">{t("usage.none")}</span>;
    if (by === "bot") return botName[key] ?? <span className="font-mono text-xs">{key.slice(0, 8)}</span>;
    if (by === "run")
      return (
        <Link to="/runs/$runId" params={{ runId: key }} className="font-mono text-xs underline underline-offset-2">
          {key.slice(0, 8)}
        </Link>
      );
    if (by === "step" && has(`run.step.${key}`)) return t(`run.step.${key}`);
    if (by === "role" && has(`models.roles.${key}.name`)) return t(`models.roles.${key}.name`);
    if (by === "model") return <span className="font-mono text-xs">{key}</span>;
    return <span className="figures !text-sm">{key}</span>;
  };

  return (
    <div>
      <Section
        id="usage"
        title={t("usage.title")}
        aside={
          <>
            <label htmlFor="usage-days" className="sr-only">
              {t("usage.range")}
            </label>
            <NativeSelect id="usage-days" value={String(days)} onChange={(v) => setDays(Number(v))} className="w-32">
              {RANGES.map((d) => (
                <option key={d} value={d}>
                  {t("usage.days", { n: d })}
                </option>
              ))}
            </NativeSelect>
          </>
        }
      >
        <div role="tablist" aria-label={t("usage.by")} className="mb-4 flex flex-wrap gap-1">
          {DIMS.map((d) => (
            <button
              key={d}
              type="button"
              role="tab"
              aria-selected={by === d}
              aria-controls="usage-table"
              onClick={() => setBy(d)}
              className={cn(
                "h-7 rounded-sm border px-2.5 text-sm transition-colors duration-[var(--dur-1)]",
                by === d
                  ? "border-ink bg-ink text-surface"
                  : "border-rule-strong bg-surface-raised text-ink-soft hover:bg-surface-sunk hover:text-ink",
              )}
            >
              {t(`usage.dims.${d}`)}
            </button>
          ))}
        </div>
        {usage.isError && (
          <Notice icon={CircleAlert} tone="danger" title={t("usage.loadFailed")}>
            {errorText(usage.error)}
          </Notice>
        )}
        {usage.isSuccess && rows.length === 0 && (
          <Notice icon={Coins} title={t("usage.empty.title")}>
            {t("usage.empty.body")}
          </Notice>
        )}
        {rows.length > 0 && (
          <div id="usage-table" role="tabpanel" className="overflow-x-auto">
            <table className={cn("w-full min-w-[34rem] text-sm", usage.isFetching && "opacity-70")}>
              <thead>
                <tr className="border-b border-rule-strong text-left">
                  <th scope="col" className="label-caps py-1.5 pr-3 font-[var(--weight-strong)]">
                    {t(`usage.dims.${by}`)}
                  </th>
                  {["calls", "tokensIn", "tokensOut", "time", "cost"].map((c) => (
                    <th key={c} scope="col" className="label-caps py-1.5 pl-3 text-right font-[var(--weight-strong)]">
                      {t(`usage.col.${c}`)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.key || "-"} className="border-b border-rule">
                    <td className="max-w-64 truncate py-1.5 pr-3 text-ink">{keyCell(r.key)}</td>
                    <td className="figures py-1.5 pl-3 text-right text-ink-soft">{r.calls}</td>
                    <td className="figures py-1.5 pl-3 text-right text-ink-soft">{formatTokens(r.tokens_in)}</td>
                    <td className="figures py-1.5 pl-3 text-right text-ink-soft">{formatTokens(r.tokens_out)}</td>
                    <td className="figures py-1.5 pl-3 text-right text-ink-soft">{formatDuration(r.seconds * 1000)}</td>
                    <td className="figures py-1.5 pl-3 text-right text-ink">{formatUSD(r.cost_usd)}</td>
                  </tr>
                ))}
              </tbody>
              <tfoot>
                <tr className="border-t border-rule-strong">
                  <th scope="row" className="py-1.5 pr-3 text-left text-sm font-[var(--weight-strong)] text-ink">
                    {t("usage.total")}
                  </th>
                  <td className="figures py-1.5 pl-3 text-right text-ink">{total.calls}</td>
                  <td className="figures py-1.5 pl-3 text-right text-ink">{formatTokens(total.tin)}</td>
                  <td className="figures py-1.5 pl-3 text-right text-ink">{formatTokens(total.tout)}</td>
                  <td className="figures py-1.5 pl-3 text-right text-ink">{formatDuration(total.sec * 1000)}</td>
                  <td className="figures py-1.5 pl-3 text-right font-[var(--weight-strong)] text-ink">
                    {formatUSD(total.cost)}
                  </td>
                </tr>
              </tfoot>
            </table>
          </div>
        )}
      </Section>
    </div>
  );
}
