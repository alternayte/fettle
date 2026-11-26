import { useEffect, useState } from "react";
import { CircleCheck, CircleDashed, CircleSlash, CircleX, Hourglass, ShieldAlert, ChevronRight } from "lucide-react";
import { t, tn } from "@/lib/copy";
import { formatDuration, formatUSD } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Activity, RunLine as RunLineT } from "./types";

const LIVE = new Set(["queued", "running", "waiting", "verifying"]);

/** useElapsed returns the whole seconds since an ISO time, updated each second. */
function useElapsed(since: string): number {
  const start = Date.parse(since) || Date.now();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [since]);
  return Math.max(0, Math.floor((now - start) / 1000));
}

/** ActivityLine is the live step of a run: one label that changes as the bot works. */
export function ActivityLine({ activity, onOpen }: { activity: Activity; onOpen: () => void }) {
  const seconds = useElapsed(activity.started_at);
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={t("run.activity.open", { label: activity.label })}
      data-testid="activity-line"
      className="mt-1 flex w-full max-w-[var(--measure)] items-center gap-2 px-2.5 text-left text-xs text-ink-soft hover:text-ink"
    >
      <span aria-hidden className="relative flex size-1.5 shrink-0">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-info opacity-60" />
        <span className="relative inline-flex size-1.5 rounded-full bg-info" />
      </span>
      <span className="min-w-0 truncate" aria-live="polite">
        {activity.label}
      </span>
      {seconds >= 2 && (
        <span className="figures shrink-0 text-ink-faint">{t("run.activity.elapsed", { seconds })}</span>
      )}
    </button>
  );
}

function stateView(r: RunLineT) {
  switch (r.state) {
    case "done":
      return r.verified
        ? { icon: CircleCheck, label: t("run.state.verified"), tone: "text-ok" }
        : { icon: CircleCheck, label: t("run.state.done"), tone: "text-ink-soft" };
    case "failed":
      return { icon: CircleX, label: t("run.state.failed"), tone: "text-danger" };
    case "escalated":
      return { icon: ShieldAlert, label: t("run.state.escalated"), tone: "text-warn" };
    case "cancelled":
      return { icon: CircleSlash, label: t("run.state.cancelled"), tone: "text-ink-faint" };
    case "waiting":
      return { icon: Hourglass, label: t("run.state.waiting"), tone: "text-warn" };
    case "verifying":
      return { icon: CircleDashed, label: t("run.state.verifying"), tone: "text-info" };
    default:
      return { icon: CircleDashed, label: t("run.state.running"), tone: "text-info" };
  }
}

export function RunLine({ run, onOpen, active }: { run: RunLineT; onOpen: (runId: string) => void; active?: boolean }) {
  const s = stateView(run);
  const Icon = s.icon;
  const live = run.state === "running" || run.state === "verifying";
  const parts = [
    t(`run.step.${run.step}`),
    run.helpers > 0 ? tn("run.helpers", run.helpers) : null,
    formatDuration(run.duration_ms),
    formatUSD(run.cost_usd),
  ].filter(Boolean);
  const activity = run.activity && LIVE.has(run.state) ? run.activity : undefined;
  // A run line without a label is a placeholder until the server sends the real line.
  if (!run.label) return activity ? <ActivityLine activity={activity} onOpen={() => onOpen(run.run_id)} /> : null;
  return (
    <>
      <button
        type="button"
        onClick={() => onOpen(run.run_id)}
        aria-label={t("run.open", { label: run.label })}
        className={cn(
          "group mt-2 flex w-full max-w-[var(--measure)] items-center gap-2 rounded-sm border border-rule bg-surface px-2.5 py-1.5 text-left",
          "transition-[border-color,background-color] duration-[var(--dur-2)] ease-[var(--ease-settle)] hover:border-rule-strong hover:bg-surface-raised",
          active && "border-rule-strong bg-surface-raised",
        )}
      >
        <Icon
          aria-hidden
          className={cn("size-3.5 shrink-0", s.tone, live && "animate-spin [animation-duration:2.4s]")}
        />
        <span className="min-w-0 flex-1 truncate text-sm font-[var(--weight-medium)] text-ink">{run.label}</span>
        <span className="figures hidden shrink-0 whitespace-nowrap text-ink-faint sm:inline">{parts.join(" · ")}</span>
        <span className={cn("shrink-0 text-xs font-[var(--weight-medium)]", s.tone)}>{s.label}</span>
        <ChevronRight
          aria-hidden
          className="size-3.5 shrink-0 text-ink-faint transition-transform duration-[var(--dur-2)] group-hover:translate-x-0.5"
        />
      </button>
      {activity && <ActivityLine activity={activity} onOpen={() => onOpen(run.run_id)} />}
    </>
  );
}

/** RunStateLabel shows the state of a run as an icon and a word. */
export function RunStateLabel({ run, className }: { run: Pick<RunLineT, "state" | "verified">; className?: string }) {
  const s = stateView(run as RunLineT);
  const Icon = s.icon;
  const live = run.state === "running" || run.state === "verifying";
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-xs font-[var(--weight-medium)]", s.tone, className)}>
      <Icon aria-hidden className={cn("size-3.5 shrink-0", live && "animate-spin [animation-duration:2.4s]")} />
      {s.label}
    </span>
  );
}
