import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { motion, useReducedMotion } from "motion/react";
import {
  Check,
  CircleHelp,
  Coins,
  Hand,
  KeyRound,
  ListChecks,
  ListX,
  Monitor,
  ShieldQuestion,
  TriangleAlert,
  UserPlus,
  X,
} from "lucide-react";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import type { Card } from "./types";

export type CardAnswer = { action: string; value?: string };

const icons = {
  approval: ShieldQuestion,
  question: CircleHelp,
  budget: Coins,
  uncertain: TriangleAlert,
  takeover: Hand,
  turn_limit: TriangleAlert,
  change: KeyRound,
  warning: TriangleAlert,
  verification: ListX,
  check: ListChecks,
  proposal: UserPlus,
} as const;

type Proposed = { name: string; job: string; instructions: string; [k: string]: unknown };

/** ProposalEditor changes the proposed bots before Create (§9.8). */
function ProposalEditor({
  raw,
  onCreate,
  onCancel,
}: {
  raw: string;
  onCreate: (v: string) => void;
  onCancel: () => void;
}) {
  const [list, setList] = useState<Proposed[]>(() => {
    try {
      return JSON.parse(raw) as Proposed[];
    } catch {
      return [];
    }
  });
  const set = (i: number, k: "name" | "job" | "instructions", v: string) =>
    setList((l) => l.map((p, j) => (j === i ? { ...p, [k]: v } : p)));
  const field = "mt-0.5 w-full rounded-xs border border-rule-strong bg-surface px-2 py-1 text-sm text-ink";
  return (
    <form
      className="space-y-3 px-3 py-2.5 pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        onCreate(JSON.stringify(list));
      }}
    >
      {list.map((p, i) => (
        <fieldset key={i} className="space-y-1.5">
          <label className="block text-xs text-ink-soft">
            {t("card.proposal.name")}
            <input value={p.name} maxLength={40} onChange={(e) => set(i, "name", e.target.value)} className={field} />
          </label>
          <label className="block text-xs text-ink-soft">
            {t("card.proposal.job")}
            <input value={p.job} onChange={(e) => set(i, "job", e.target.value)} className={field} />
          </label>
          <label className="block text-xs text-ink-soft">
            {t("card.proposal.instructions")}
            <textarea
              rows={4}
              value={p.instructions}
              onChange={(e) => set(i, "instructions", e.target.value)}
              className={cn(field, "font-mono text-xs")}
            />
          </label>
        </fieldset>
      ))}
      <div className="flex gap-1.5">
        <ActionButton kind="act" type="submit">
          {t("card.proposal.create")}
        </ActionButton>
        <ActionButton kind="quiet" onClick={onCancel}>
          {t("common.cancel")}
        </ActionButton>
      </div>
    </form>
  );
}

function ActionButton({
  children,
  kind = "plain",
  onClick,
  disabled,
  type = "button",
}: {
  children: React.ReactNode;
  kind?: "act" | "plain" | "quiet";
  onClick?: () => void;
  disabled?: boolean;
  type?: "button" | "submit";
}) {
  return (
    <button
      type={type}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 items-center gap-1.5 rounded-sm px-2.5 text-sm font-[var(--weight-medium)] transition-colors duration-[var(--dur-1)]",
        "disabled:pointer-events-none disabled:opacity-50",
        kind === "act" && "bg-glaze text-glaze-ink hover:brightness-105",
        kind === "plain" && "border border-rule-strong bg-surface-raised text-ink hover:bg-surface-sunk",
        kind === "quiet" && "text-ink-soft hover:bg-surface-sunk hover:text-ink",
      )}
    >
      {children}
    </button>
  );
}

function Resolved({ card }: { card: Card }) {
  const denied = card.status === "denied" || card.status === "expired";
  const Icon = denied ? X : Check;
  const label =
    card.status === "allowed"
      ? t("card.resolved.allowed")
      : card.status === "denied"
        ? t("card.resolved.denied")
        : card.status === "expired"
          ? t("card.resolved.expired")
          : t("card.resolved.answered", { answer: card.answer ?? "" });
  return (
    <div className="mt-2 flex max-w-[var(--measure)] items-center gap-2 border-l-2 border-rule-strong py-0.5 pl-3 text-sm text-ink-soft">
      <Icon aria-hidden className={cn("size-3.5", denied ? "text-danger" : "text-ok")} />
      <span className="font-[var(--weight-medium)] text-ink">{card.title}</span>
      <span>· {label}</span>
    </div>
  );
}

export function CardView({ card, onAnswer }: { card: Card; onAnswer: (a: CardAnswer) => void }) {
  const reduce = useReducedMotion();
  const [editing, setEditing] = useState(false);
  const [answer, setAnswer] = useState("");
  if (card.status !== "pending") return <Resolved card={card} />;
  const Icon = icons[card.kind];
  return (
    <motion.section
      initial={reduce ? false : { opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.24, ease: [0.22, 1, 0.36, 1] }}
      aria-label={card.title}
      data-card-id={card.id}
      tabIndex={-1}
      className="relative mt-2 max-w-[var(--measure)] overflow-hidden rounded-sm border border-rule-strong bg-surface-raised shadow-[var(--shadow-1)]"
    >
      <span aria-hidden className="absolute inset-y-0 left-0 w-[3px] bg-glaze" />
      <header className="flex items-center gap-2 border-b border-rule px-3 py-2 pl-4">
        <Icon aria-hidden className="size-4 text-glaze" />
        <h3 className="text-sm font-[var(--weight-strong)] text-ink">{card.title}</h3>
        <span className="label-caps ml-auto">{t(`card.kind.${card.kind}`)}</span>
      </header>
      <div className="space-y-2 px-3 py-2.5 pl-4">
        {card.detail && <p className="text-sm whitespace-pre-line text-ink-soft">{card.detail}</p>}
        {card.tool && (
          <p className="text-sm text-ink-soft">
            {t("card.approval.tool")} <code className="font-mono text-xs text-ink">{card.tool}</code>
          </p>
        )}
        {card.preview && (
          <dl className="grid grid-cols-[minmax(4.5rem,auto)_1fr] gap-x-3 gap-y-1 border-t border-dashed border-rule pt-2 text-sm">
            {Object.entries(card.preview).map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="figures pt-0.5 text-ink-faint">{k}</dt>
                <dd className="min-w-0 break-words text-ink">{v}</dd>
              </div>
            ))}
          </dl>
        )}
        {card.hard_ask && <p className="text-xs text-ink-faint">{t("card.approval.hardAsk")}</p>}
      </div>
      {editing && card.kind === "proposal" && (
        <ProposalEditor
          raw={card.data?.proposal ?? "[]"}
          onCancel={() => setEditing(false)}
          onCreate={(v) => onAnswer({ action: "create", value: v })}
        />
      )}
      <footer className="flex flex-wrap items-center gap-1.5 border-t border-rule bg-surface px-3 py-2 pl-4">
        {card.kind === "approval" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "allow" })}>
              {t("card.approval.allowOnce")}
            </ActionButton>
            {!card.hard_ask && (
              <ActionButton onClick={() => onAnswer({ action: "always" })}>{t("card.approval.always")}</ActionButton>
            )}
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "deny" })}>
              {t("card.approval.deny")}
            </ActionButton>
          </>
        )}
        {card.kind === "change" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "allow" })}>
              {t("card.change.allow")}
            </ActionButton>
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "deny" })}>
              {t("card.change.deny")}
            </ActionButton>
          </>
        )}
        {card.kind === "warning" && (
          <ActionButton onClick={() => onAnswer({ action: "dismiss" })}>{t("card.warning.dismiss")}</ActionButton>
        )}
        {card.kind === "question" && (card.options ?? []).length === 0 && (
          <form
            className="flex w-full min-w-0 flex-1 gap-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              if (answer.trim()) onAnswer({ action: "answer", value: answer.trim() });
            }}
          >
            <input
              aria-label={t("card.question.answer")}
              value={answer}
              onChange={(e) => setAnswer(e.target.value)}
              maxLength={2000}
              className="h-7 min-w-0 flex-1 rounded-sm border border-rule-strong bg-surface-raised px-2 text-sm text-ink"
            />
            <ActionButton kind="act" type="submit" disabled={!answer.trim()}>
              {t("card.question.send")}
            </ActionButton>
          </form>
        )}
        {card.kind === "question" &&
          (card.options ?? []).map((o, i) => (
            <ActionButton
              key={o}
              kind={i === 0 ? "act" : "plain"}
              onClick={() => onAnswer({ action: "answer", value: o })}
            >
              {o}
            </ActionButton>
          ))}
        {card.kind === "budget" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "add_budget", value: "1" })}>
              {t("card.budget.add", { usd: "$1" })}
            </ActionButton>
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "stop" })}>
              {t("card.budget.stop")}
            </ActionButton>
          </>
        )}
        {card.kind === "uncertain" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "done" })}>
              {t("card.uncertain.happened")}
            </ActionButton>
            <ActionButton onClick={() => onAnswer({ action: "not_done" })}>{t("card.uncertain.didNot")}</ActionButton>
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "stop" })}>
              {t("card.uncertain.stop")}
            </ActionButton>
          </>
        )}
        {card.kind === "turn_limit" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "continue" })}>
              {t("card.turnLimit.continue")}
            </ActionButton>
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "stop" })}>
              {t("card.turnLimit.stop")}
            </ActionButton>
          </>
        )}
        {card.kind === "proposal" && !editing && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "create" })}>
              {t("card.proposal.create")}
            </ActionButton>
            <ActionButton onClick={() => setEditing(true)}>{t("card.proposal.edit")}</ActionButton>
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "decline" })}>
              {t("card.proposal.decline")}
            </ActionButton>
          </>
        )}
        {card.kind === "verification" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "retry" })}>
              {t("card.verification.retry")}
            </ActionButton>
            <ActionButton onClick={() => onAnswer({ action: "accept" })}>{t("card.verification.accept")}</ActionButton>
            <ActionButton kind="quiet" onClick={() => onAnswer({ action: "stop" })}>
              {t("card.verification.stop")}
            </ActionButton>
          </>
        )}
        {card.kind === "check" && (
          <>
            <ActionButton kind="act" onClick={() => onAnswer({ action: "pass" })}>
              {t("card.check.pass")}
            </ActionButton>
            <ActionButton onClick={() => onAnswer({ action: "fail" })}>{t("card.check.fail")}</ActionButton>
          </>
        )}
        {card.kind === "takeover" && card.data?.bot_id && (
          <Link
            to="/screen/$botId"
            params={{ botId: card.data.bot_id }}
            search={{ card: card.id }}
            className="inline-flex h-8 items-center gap-1.5 rounded-sm bg-glaze px-3 text-sm font-[var(--weight-medium)] text-glaze-ink hover:brightness-105"
          >
            <Monitor aria-hidden className="size-3.5" />
            {t("card.takeover.open")}
          </Link>
        )}
        <span className="figures ml-auto hidden text-ink-faint sm:inline">{t("card.waits")}</span>
      </footer>
    </motion.section>
  );
}
