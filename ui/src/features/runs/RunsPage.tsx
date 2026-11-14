import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Activity, BookOpen, CircleAlert, Square } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { LIVE_STATES, useBots, useRun, useRuns, useSession, useStopRun } from "@/api/hooks";
import { Notice, Page } from "@/app/Page";
import { useShell } from "@/app/shell-context";
import { Btn } from "@/components/form";
import { NativeSelect } from "@/features/settings/ProviderForm";
import { Skeleton } from "@/components/ui/skeleton";
import { ChatHeader } from "@/features/chat/ChatView";
import { RunStateLabel } from "@/features/chat/RunLine";
import { toRunLine } from "@/features/chat/map";
import { has, t, tn } from "@/lib/copy";
import { formatDay, formatClock, formatDuration, formatUSD } from "@/lib/format";
import { Timeline } from "./Timeline";
import { toRunDetail } from "./map";

const stepLabel = (step: string) => (has(`run.step.${step}`) ? t(`run.step.${step}`) : step);

export function RunsPage() {
  const [bot, setBot] = useState("");
  const runs = useRuns(bot || undefined);
  const bots = useBots();
  return (
    <Page
      title={t("runs.title")}
      wide
      actions={
        <>
          <label htmlFor="runs-bot" className="sr-only">
            {t("runs.filterBot")}
          </label>
          <NativeSelect id="runs-bot" value={bot} onChange={setBot} className="w-36 [&_select]:h-8 [&_select]:text-sm">
            <option value="">{t("runs.allBots")}</option>
            {(bots.data ?? []).map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </NativeSelect>
        </>
      }
    >
      {runs.isPending && (
        <div className="space-y-2" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 bg-surface-sunk" />
          ))}
        </div>
      )}
      {runs.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("runs.loadFailed")}>
          {errorText(runs.error)}
        </Notice>
      )}
      {runs.isSuccess && runs.data.length === 0 && (
        <Notice icon={Activity} title={t("runs.empty.title")}>
          {t("runs.empty.body")}
        </Notice>
      )}
      {runs.isSuccess && runs.data.length > 0 && (
        <ol className="border-t border-rule-strong" aria-label={t("runs.title")}>
          {runs.data.map((r) => {
            const line = toRunLine(r.line);
            const started = r.started_at;
            return (
              <li key={line.run_id} className="border-b border-rule">
                <Link
                  to="/runs/$runId"
                  params={{ runId: line.run_id }}
                  className="grid grid-cols-[1fr_auto] items-start gap-x-4 gap-y-0.5 px-1 py-2.5 hover:bg-surface-sunk sm:grid-cols-[7rem_1fr_auto_auto] sm:px-2"
                >
                  <RunStateLabel
                    run={line}
                    className="order-2 justify-self-end pt-px sm:order-none sm:justify-self-start"
                  />
                  <span className="order-1 min-w-0 sm:order-none">
                    <span className="block truncate text-sm font-[var(--weight-medium)] text-ink">{line.label}</span>
                    <span className="block truncate text-xs text-ink-faint">
                      {r.bot_name} · {stepLabel(line.step)}
                      {line.helpers > 0 && ` · ${tn("run.helpers", line.helpers)}`}
                    </span>
                  </span>
                  <span className="figures order-3 pt-0.5 text-ink-faint sm:order-none sm:text-right">
                    {formatDuration(line.duration_ms)} · {formatUSD(line.cost_usd)}
                  </span>
                  <span className="figures order-4 pt-0.5 text-right text-ink-faint sm:order-none">
                    {started ? `${formatDay(started)} ${formatClock(started)}` : t("run.state.queued")}
                  </span>
                </Link>
              </li>
            );
          })}
        </ol>
      )}
    </Page>
  );
}

export function RunPage() {
  const { runId } = useParams({ strict: false }) as { runId: string };
  const shell = useShell();
  const navigate = useNavigate();
  const q = useRun(runId);
  const stop = useStopRun();
  const { isOwner } = useSession();
  const detail = useMemo(() => (q.data ? toRunDetail(q.data) : null), [q.data]);
  const live = !!q.data && LIVE_STATES.has(q.data.line.state);
  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col">
      <ChatHeader title={t("runs.title")} subtitle={detail?.label} onMenu={shell.openMenu}>
        {live && (
          <Btn
            size="sm"
            busy={stop.isPending}
            onClick={() =>
              stop.mutate(runId, {
                onError: (err) => toast.error(t("run.stopFailed"), { description: errorText(err) }),
              })
            }
          >
            <Square aria-hidden className="size-3 fill-current" />
            {t("chat.composer.stop")}
          </Btn>
        )}
        {q.data?.line.state === "done" && isOwner && (
          <Btn size="sm" kind="quiet" onClick={() => void navigate({ to: "/skills", search: { from_run: runId } })}>
            <BookOpen aria-hidden className="size-3.5" />
            {t("skills.saveFromRun")}
          </Btn>
        )}
      </ChatHeader>
      <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col sm:border-x sm:border-rule">
        {detail && <Timeline run={detail} onClose={() => void navigate({ to: "/runs" })} />}
        {q.isPending && (
          <p className="p-4 text-sm text-ink-faint" role="status">
            {t("common.loading")}
          </p>
        )}
        {q.isError && (
          <div className="p-4">
            <Notice icon={CircleAlert} tone="danger" title={t("timeline.loadFailed")}>
              {errorText(q.error)}
            </Notice>
          </div>
        )}
      </div>
    </section>
  );
}
