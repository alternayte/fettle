import { useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, X } from "lucide-react";
import { t, tn } from "@/lib/copy";
import { formatClockPrecise, formatDuration, formatTokens, formatUSD } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { RunDetail, RunEvent } from "./types";

// Event families decide the margin glyph. Shape and text carry meaning; colour only supports it.
const family: Record<string, { glyph: string; tone: string }> = {
  run_started: { glyph: "○", tone: "text-ink-faint" },
  run_finished: { glyph: "●", tone: "text-ink" },
  route_decision: { glyph: "◇", tone: "text-ink-soft" },
  context_built: { glyph: "·", tone: "text-ink-faint" },
  model_turn: { glyph: "▸", tone: "text-ink-soft" },
  model_switch: { glyph: "⇄", tone: "text-warn" },
  tool_call: { glyph: "→", tone: "text-ink-soft" },
  tool_intent: { glyph: "⇢", tone: "text-warn" },
  tool_result: { glyph: "←", tone: "text-ink-soft" },
  tool_uncertain: { glyph: "?", tone: "text-danger" },
  approval_requested: { glyph: "◆", tone: "text-glaze" },
  approval_decided: { glyph: "◆", tone: "text-ink-soft" },
  subagents_started: { glyph: "⑂", tone: "text-info" },
  subagent_result: { glyph: "⑂", tone: "text-ink-soft" },
  verify_started: { glyph: "✓", tone: "text-info" },
  verify_result: { glyph: "✓", tone: "text-ok" },
  steer: { glyph: "✎", tone: "text-glaze" },
  budget_reached: { glyph: "$", tone: "text-danger" },
  cancel: { glyph: "×", tone: "text-danger" },
  summary: { glyph: "≡", tone: "text-ink-faint" },
  memory_write: { glyph: "✱", tone: "text-ink-soft" },
};

function EventRow({ ev, open, onToggle }: { ev: RunEvent; open: boolean; onToggle: () => void }) {
  const f = family[ev.type] ?? { glyph: "·", tone: "text-ink-faint" };
  const meta = [
    ev.duration_ms !== undefined ? formatDuration(ev.duration_ms) : null,
    ev.tokens !== undefined ? t("timeline.tokens", { n: formatTokens(ev.tokens) }) : null,
    ev.cost_usd !== undefined ? formatUSD(ev.cost_usd) : null,
  ].filter(Boolean);
  return (
    <div
      className={cn(
        "grid grid-cols-[2.75rem_1rem_1fr] gap-x-2 border-b border-rule/70 px-3 py-1.5",
        ev.lane && "bg-surface-sunk/40",
      )}
    >
      <span className="figures pt-0.5 text-right text-ink-faint">{ev.seq}</span>
      <span aria-hidden className={cn("pt-px text-center font-mono text-sm leading-5", f.tone)}>
        {f.glyph}
      </span>
      <div className="min-w-0">
        <button
          type="button"
          onClick={onToggle}
          disabled={!ev.detail}
          aria-expanded={ev.detail ? open : undefined}
          className="flex w-full min-w-0 items-baseline gap-2 text-left disabled:cursor-default"
        >
          <span className="label-caps shrink-0 !text-ink-soft">{t(`timeline.type.${ev.type}`)}</span>
          {ev.lane && (
            <span className="figures shrink-0 rounded-xs border border-rule px-1 text-ink-faint">{ev.lane}</span>
          )}
          <span className="min-w-0 truncate text-sm text-ink">{ev.summary}</span>
          {ev.detail && (
            <ChevronDown
              aria-hidden
              className={cn(
                "ml-auto size-3 shrink-0 self-center text-ink-faint transition-transform duration-[var(--dur-2)]",
                open && "rotate-180",
              )}
            />
          )}
        </button>
        <div className="figures flex gap-2 text-ink-faint">
          <time dateTime={ev.at}>{formatClockPrecise(ev.at)}</time>
          {meta.length > 0 && <span>{meta.join(" · ")}</span>}
        </div>
        {open && ev.detail && (
          <pre className="mt-1.5 max-h-64 overflow-auto rounded-xs border border-rule bg-surface-sunk p-2 font-mono text-xs whitespace-pre-wrap text-ink-soft">
            {ev.detail}
          </pre>
        )}
      </div>
    </div>
  );
}

export function TimelineHeader({ run, onClose }: { run: RunDetail; onClose?: () => void }) {
  const stat = (label: string, value: string) => (
    <div className="min-w-0">
      <dt className="label-caps">{label}</dt>
      <dd className="figures truncate !text-sm text-ink">{value}</dd>
    </div>
  );
  return (
    <header className="shrink-0 border-b border-rule bg-surface px-4 pt-3 pb-3">
      <div className="flex items-start gap-2">
        <div className="min-w-0">
          <p className="label-caps">{t("timeline.title")}</p>
          <h2 className="serif-title truncate text-lg text-ink">{run.label}</h2>
          <p className="text-xs text-ink-faint">
            {run.bot_name} · {t(`run.step.${run.step}`)}
            {run.helpers > 0 && ` · ${tn("run.helpers", run.helpers)}`}
          </p>
        </div>
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label={t("timeline.close")}
            className="ml-auto inline-flex size-7 shrink-0 items-center justify-center rounded-sm text-ink-faint hover:bg-surface-sunk hover:text-ink"
          >
            <X className="size-4" aria-hidden />
          </button>
        )}
      </div>
      <dl className="mt-3 grid grid-cols-4 gap-3">
        {stat(
          t("timeline.stat.state"),
          t(`run.state.${run.state === "done" && run.verified ? "verified" : run.state}`),
        )}
        {stat(t("timeline.stat.time"), formatDuration(run.duration_ms))}
        {stat(t("timeline.stat.cost"), formatUSD(run.cost_usd))}
        {stat(t("timeline.stat.tokens"), formatTokens(run.tokens_in + run.tokens_out))}
      </dl>
    </header>
  );
}

export function Timeline({ run, onClose }: { run: RunDetail; onClose?: () => void }) {
  const parent = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const v = useVirtualizer({
    count: run.events.length,
    getScrollElement: () => parent.current,
    estimateSize: () => 46,
    overscan: 12,
  });
  return (
    <div className="flex h-full min-h-0 flex-col bg-ground">
      <TimelineHeader run={run} onClose={onClose} />
      <div ref={parent} className="min-h-0 flex-1 overflow-y-auto" data-testid="timeline-scroller">
        <div style={{ height: v.getTotalSize(), position: "relative" }} role="list" aria-label={t("timeline.events")}>
          {v.getVirtualItems().map((item) => {
            const ev = run.events[item.index];
            return (
              <div
                key={ev.seq}
                role="listitem"
                ref={v.measureElement}
                data-index={item.index}
                style={{ position: "absolute", top: 0, left: 0, right: 0, transform: `translateY(${item.start}px)` }}
              >
                <EventRow
                  ev={ev}
                  open={open.has(ev.seq)}
                  onToggle={() =>
                    setOpen((s) => {
                      const n = new Set(s);
                      if (n.has(ev.seq)) n.delete(ev.seq);
                      else n.add(ev.seq);
                      return n;
                    })
                  }
                />
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
