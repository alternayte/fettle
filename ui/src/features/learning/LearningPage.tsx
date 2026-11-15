import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { CircleAlert, GitCompare, Lightbulb, Signal } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useSetSetting } from "@/api/hooks";
import { Notice, Page, Section } from "@/app/Page";
import { Btn } from "@/components/form";
import { has, t, tn } from "@/lib/copy";
import { MarkdownInline } from "@/features/chat/Markdown";
import { formatDay, formatClock, formatDuration, formatUSD } from "@/lib/format";
import { useRouterReport, useRunPatchJob, useScores, useSignals } from "./api";

const stepLabel = (s: string) => (has(`run.step.${s}`) ? t(`run.step.${s}`) : s);
const signalLabel = (s: string) => (has(`learning.signal.${s}`) ? t(`learning.signal.${s}`) : s);
const pct = (n: number) => `${Math.round(n * 100)} %`;

const th = "label-caps py-1.5 pr-3 text-left font-[var(--weight-strong)]";
const thNum = "label-caps py-1.5 pl-3 text-right font-[var(--weight-strong)]";
const td = "py-1.5 pr-3 text-ink";
const tdNum = "figures py-1.5 pl-3 text-right text-ink-soft";

function RouterReport() {
  const [weeksAgo, setWeeksAgo] = useState(0);
  const rep = useRouterReport(weeksAgo);
  const set = useSetSetting();
  return (
    <Section
      id="router"
      title={t("learning.router.title")}
      aside={
        <div className="flex gap-1">
          <Btn kind="quiet" onClick={() => setWeeksAgo((w) => w + 1)}>
            {t("learning.router.older")}
          </Btn>
          <Btn kind="quiet" disabled={weeksAgo === 0} onClick={() => setWeeksAgo((w) => Math.max(0, w - 1))}>
            {t("learning.router.newer")}
          </Btn>
        </div>
      }
    >
      {rep.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("learning.loadFailed")}>
          {errorText(rep.error)}
        </Notice>
      )}
      {rep.data && (
        <>
          <p className="mb-3 text-sm text-ink-soft">
            {t("learning.router.period", { from: formatDay(rep.data.from), to: formatDay(rep.data.to) })}
          </p>
          {(rep.data.steps ?? []).length === 0 ? (
            <Notice title={t("learning.router.empty")} />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[34rem] text-sm">
                <thead>
                  <tr className="border-b border-rule-strong">
                    <th scope="col" className={th}>
                      {t("learning.router.step")}
                    </th>
                    <th scope="col" className={thNum}>
                      {t("learning.router.runs")}
                    </th>
                    <th scope="col" className={thNum}>
                      {t("learning.router.passRate")}
                    </th>
                    <th scope="col" className={thNum}>
                      {t("learning.router.score")}
                    </th>
                    <th scope="col" className={thNum}>
                      {t("learning.router.cost")}
                    </th>
                    <th scope="col" className={thNum}>
                      {t("learning.router.duration")}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {(rep.data.steps ?? []).map((s) => (
                    <tr key={s.step} className="border-b border-rule">
                      <th scope="row" className={`${td} text-left font-[var(--weight-medium)]`}>
                        {stepLabel(s.step)}
                      </th>
                      <td className={tdNum}>{s.runs}</td>
                      <td className={tdNum}>{pct(s.pass_rate)}</td>
                      <td className={tdNum}>{Math.round(s.avg_score)}</td>
                      <td className={tdNum}>{formatUSD(s.avg_cost_usd)}</td>
                      <td className={tdNum}>{formatDuration(s.avg_duration_ms)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {(rep.data.suggestions ?? []).length > 0 && (
            <ul className="mt-4 space-y-2">
              {(rep.data.suggestions ?? []).map((s) => (
                <li
                  key={s.setting + s.value}
                  className="flex flex-wrap items-start gap-3 rounded-sm border border-rule bg-surface-raised p-3"
                >
                  <Lightbulb aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-faint" />
                  <div className="min-w-0 flex-1 text-sm">
                    <p className="text-ink">{s.reason}</p>
                    <p className="figures mt-1 text-xs text-ink-faint">
                      {s.setting} = {s.value}
                    </p>
                  </div>
                  <Btn
                    busy={set.isPending}
                    onClick={() =>
                      set.mutate(
                        { key: s.setting, value: s.value },
                        {
                          onSuccess: () => toast(t("learning.router.applied", { setting: s.setting, value: s.value })),
                          onError: (err) => toast.error(errorText(err)),
                        },
                      )
                    }
                  >
                    {t("learning.router.apply")}
                  </Btn>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </Section>
  );
}

function Averages({ title, rows }: { title: string; rows: { key: string; runs: number; avg_score: number }[] }) {
  return (
    <div className="min-w-0">
      <h3 className="label-caps pb-1">{title}</h3>
      {rows.length === 0 ? (
        <p className="py-1.5 text-sm text-ink-faint">{t("learning.scores.none")}</p>
      ) : (
        <table className="w-full text-sm">
          <tbody>
            {rows.slice(0, 8).map((r) => (
              <tr key={r.key} className="border-b border-rule">
                <th scope="row" className={`${td} max-w-40 truncate text-left font-normal`}>
                  {r.key}
                </th>
                <td className={tdNum}>{tn("learning.scores.runs", r.runs)}</td>
                <td className={`${tdNum} text-ink`}>{Math.round(r.avg_score)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function Scores() {
  const scores = useScores(30);
  const d = scores.data;
  return (
    <Section id="scores" title={t("learning.scores.title")}>
      {d && (
        <>
          <p className="mb-4 text-sm text-ink-soft">
            {d.runs > 0
              ? tn("learning.scores.summary", d.runs, { avg: Math.round(d.average) })
              : t("learning.scores.empty")}
          </p>
          {d.runs > 0 && (
            <div className="grid gap-6 sm:grid-cols-2">
              <Averages title={t("learning.scores.byBot")} rows={d.by_bot ?? []} />
              <Averages title={t("learning.scores.bySkill")} rows={d.by_skill ?? []} />
              <Averages
                title={t("learning.scores.byStep")}
                rows={(d.by_step ?? []).map((r) => ({ ...r, key: stepLabel(r.key) }))}
              />
              <Averages title={t("learning.scores.byModel")} rows={d.by_model ?? []} />
            </div>
          )}
        </>
      )}
    </Section>
  );
}

function Patches() {
  const job = useRunPatchJob();
  return (
    <Section
      id="patches"
      title={t("learning.patches.title")}
      aside={
        <Btn
          busy={job.isPending}
          onClick={() => job.mutate(undefined, { onError: (err) => toast.error(errorText(err)) })}
        >
          <GitCompare aria-hidden className="size-3.5" />
          {t("learning.patches.run")}
        </Btn>
      }
    >
      <p className="max-w-[var(--measure)] text-sm text-ink-soft">{t("learning.patches.body")}</p>
      {job.data && (
        <ul className="mt-3 divide-y divide-rule border-y border-rule text-sm">
          {job.data.length === 0 && <li className="py-2 text-ink-faint">{t("learning.patches.none")}</li>}
          {job.data.map((o) => (
            <li key={o.skill} className="flex flex-wrap items-baseline gap-x-3 py-2">
              <span className="font-[var(--weight-medium)] text-ink">{o.skill}</span>
              <span className="label-caps">{t(`learning.patches.outcome.${o.outcome}`)}</span>
              {o.outcome !== "skipped" && (
                <span className="figures text-xs text-ink-faint">
                  {o.old_score.toFixed(2)} → {o.new_score.toFixed(2)}
                </span>
              )}
              {o.reason && <span className="w-full text-xs text-ink-soft">{o.reason}</span>}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function Signals() {
  const signals = useSignals(14);
  return (
    <Section id="signals" title={t("learning.signals.title")}>
      {signals.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("learning.loadFailed")}>
          {errorText(signals.error)}
        </Notice>
      )}
      {signals.isSuccess && signals.data.length === 0 && (
        <Notice icon={Signal} title={t("learning.signals.empty.title")}>
          {t("learning.signals.empty.body")}
        </Notice>
      )}
      {signals.isSuccess && signals.data.length > 0 && (
        <ol aria-label={t("learning.signals.title")} className="divide-y divide-rule border-y border-rule">
          {signals.data.map((s) => (
            <li key={s.id} className="grid grid-cols-[4.5rem_1fr] gap-x-3 py-2 text-sm sm:grid-cols-[6.5rem_11rem_1fr]">
              <time dateTime={s.at} className="figures text-xs text-ink-faint">
                {formatDay(s.at)} {formatClock(s.at)}
              </time>
              <span className="font-[var(--weight-medium)] text-ink">{signalLabel(s.type)}</span>
              <div className="col-span-2 min-w-0 sm:col-span-1">
                <p className="text-xs text-ink-soft">
                  {[s.bot_name, s.step && stepLabel(s.step), (s.skills ?? []).join(", ")].filter(Boolean).join(" · ")}
                  {s.run_id && (
                    <>
                      {" · "}
                      <Link to="/runs/$runId" params={{ runId: s.run_id }} className="underline underline-offset-2">
                        {t("learning.signals.run")}
                      </Link>
                    </>
                  )}
                </p>
                {s.detail && (
                  <p className="mt-0.5 line-clamp-2 break-words text-ink [&_code]:font-mono [&_code]:text-[0.92em]">
                    <MarkdownInline text={s.detail} />
                  </p>
                )}
              </div>
            </li>
          ))}
        </ol>
      )}
    </Section>
  );
}

/** LearningPage shows steering signals, run scores, skill patches and the router report (§14, owner). */
export function LearningPage() {
  return (
    <Page title={t("learning.title")} subtitle={t("learning.subtitle")} wide>
      <div className="space-y-10">
        <RouterReport />
        <Scores />
        <Patches />
        <Signals />
      </div>
    </Page>
  );
}
