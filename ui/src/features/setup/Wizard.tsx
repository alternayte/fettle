import { useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ArrowRight, Check, Laptop, Server } from "lucide-react";
import { Field, SecretInput, StateIcon, inputCls, type CheckState } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

export type { CheckState };

export interface WizardModel {
  provider: { kind: string; label: string; steps: { label: string; state: CheckState; detail?: string }[] };
  modelChecks: { key: string; state: CheckState; detail?: string }[];
  timeZone: string;
}

const STEPS = ["where", "computer", "model", "you"] as const;
type Step = (typeof STEPS)[number];

function Choice({
  selected,
  onSelect,
  icon: Icon,
  title,
  body,
}: {
  selected: boolean;
  onSelect: () => void;
  icon: typeof Laptop;
  title: string;
  body: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex w-full items-start gap-3 rounded-sm border bg-surface-raised p-3.5 text-left transition-[border-color,box-shadow] duration-[var(--dur-2)]",
        selected ? "border-ink shadow-[var(--shadow-2)]" : "border-rule-strong hover:border-ink-faint",
      )}
    >
      <Icon aria-hidden className="mt-0.5 size-5 shrink-0 text-ink-soft" />
      <span className="min-w-0">
        <span className="block text-base font-[var(--weight-strong)] text-ink">{title}</span>
        <span className="block text-sm text-ink-soft">{body}</span>
      </span>
      <span
        aria-hidden
        className={cn(
          "ml-auto mt-1 inline-flex size-4 shrink-0 items-center justify-center rounded-full border",
          selected ? "border-ink bg-ink" : "border-rule-strong",
        )}
      >
        {selected && <span className="size-1.5 rounded-full bg-surface" />}
      </span>
    </button>
  );
}

/**
 * WizardFrame is the shell of the first-run screens: the step list on the left and one animated
 * step on the right with its title, body and footer.
 */
export function WizardFrame({
  steps,
  step,
  title,
  body,
  children,
  footer,
}: {
  steps: { key: string; label: string }[];
  step: string;
  title: string;
  body: string;
  children: React.ReactNode;
  footer: React.ReactNode;
}) {
  const reduce = useReducedMotion();
  const idx = steps.findIndex((s) => s.key === step);
  return (
    <div className="flex min-h-full items-start justify-center bg-ground px-4 py-8 sm:items-center sm:py-12">
      <div className="grid w-full max-w-3xl overflow-hidden rounded-md border border-rule-strong bg-surface shadow-[var(--shadow-2)] md:grid-cols-[13rem_1fr]">
        <aside className="border-b border-rule bg-surface-sunk/60 p-4 md:border-r md:border-b-0 md:p-5">
          <p className="serif-title text-xl text-ink">{t("app.name")}</p>
          <p className="mt-0.5 text-xs text-ink-faint">{t("setup.tagline")}</p>
          <ol className="mt-5 hidden space-y-0.5 md:block">
            {steps.map((s, i) => (
              <li
                key={s.key}
                aria-current={s.key === step ? "step" : undefined}
                className={cn(
                  "flex h-[var(--row)] items-center gap-2.5 text-sm",
                  i < idx ? "text-ink-soft" : i === idx ? "font-[var(--weight-strong)] text-ink" : "text-ink-faint",
                )}
              >
                <span className="figures w-4 text-right">
                  {i < idx ? <Check aria-hidden className="ml-auto size-3 text-ok" /> : i + 1}
                </span>
                {s.label}
              </li>
            ))}
          </ol>
          <p className="figures mt-3 text-ink-faint md:hidden">
            {t("setup.stepOf", { n: idx + 1, total: steps.length })}
          </p>
        </aside>
        <main className="flex min-h-[26rem] min-w-0 flex-col p-5 sm:p-7">
          <AnimatePresence mode="wait" initial={false}>
            <motion.div
              key={step}
              initial={reduce ? false : { opacity: 0, x: 8 }}
              animate={{ opacity: 1, x: 0 }}
              exit={reduce ? undefined : { opacity: 0, x: -8 }}
              transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
              className="flex-1 space-y-5"
            >
              <header>
                <h1 className="serif-title text-3xl text-ink">{title}</h1>
                <p className="mt-1.5 text-base text-ink-soft">{body}</p>
              </header>
              {children}
            </motion.div>
          </AnimatePresence>
          <footer className="mt-6 flex items-center gap-2 border-t border-rule pt-4">{footer}</footer>
        </main>
      </div>
    </div>
  );
}

export function Wizard({ model, onFinish }: { model: WizardModel; onFinish?: () => void }) {
  const [step, setStep] = useState<Step>("where");
  const [where, setWhere] = useState<"local" | "server">("local");
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const idx = STEPS.indexOf(step);
  const next = () => (idx < STEPS.length - 1 ? setStep(STEPS[idx + 1]) : onFinish?.());

  return (
    <WizardFrame
      steps={STEPS.map((s) => ({ key: s, label: t(`setup.steps.${s}`) }))}
      step={step}
      title={t(`setup.${step}.title`)}
      body={t(`setup.${step}.body`)}
      footer={
        <>
          {idx > 0 && (
            <button
              type="button"
              onClick={() => setStep(STEPS[idx - 1])}
              className="h-9 rounded-sm px-3 text-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
            >
              {t("setup.back")}
            </button>
          )}
          <button
            type="button"
            onClick={next}
            className="ml-auto inline-flex h-9 items-center gap-1.5 rounded-sm bg-ink px-4 text-sm font-[var(--weight-medium)] text-surface hover:opacity-90"
          >
            {idx === STEPS.length - 1 ? t("setup.finish") : t("setup.continue")}
            <ArrowRight aria-hidden className="size-4" />
          </button>
        </>
      }
    >
      {step === "where" && (
        <div role="radiogroup" aria-label={t("setup.where.title")} className="space-y-2">
          <Choice
            selected={where === "local"}
            onSelect={() => setWhere("local")}
            icon={Laptop}
            title={t("setup.where.local")}
            body={t("setup.where.localBody")}
          />
          <Choice
            selected={where === "server"}
            onSelect={() => setWhere("server")}
            icon={Server}
            title={t("setup.where.server")}
            body={t("setup.where.serverBody")}
          />
        </div>
      )}

      {step === "computer" && (
        <div className="rounded-sm border border-rule bg-surface-raised">
          <div className="flex items-center gap-2 border-b border-rule px-3 py-2">
            <span className="label-caps">{t("setup.computer.detected")}</span>
            <span className="text-sm font-[var(--weight-strong)] text-ink">{model.provider.label}</span>
          </div>
          <ul>
            {model.provider.steps.map((s) => (
              <li key={s.label} className="flex items-center gap-2.5 border-b border-rule/70 px-3 py-2 last:border-b-0">
                <StateIcon s={s.state} />
                <span className="text-sm text-ink">{s.label}</span>
                {s.detail && <span className="figures ml-auto text-ink-faint">{s.detail}</span>}
              </li>
            ))}
          </ul>
          <p className="border-t border-rule px-3 py-2 text-xs text-ink-faint">{t("setup.computer.background")}</p>
        </div>
      )}

      {step === "model" && (
        <div className="space-y-4">
          <Field id="model-key" label={t("setup.model.keyLabel")} hint={t("setup.model.keyHint")}>
            <SecretInput id="model-key" value={key} onChange={setKey} placeholder="sk-or-…" />
          </Field>
          <ul className="rounded-sm border border-rule bg-surface-raised" aria-label={t("setup.model.checks")}>
            {model.modelChecks.map((c) => (
              <li key={c.key} className="flex items-center gap-2.5 border-b border-rule/70 px-3 py-2 last:border-b-0">
                <StateIcon s={c.state} />
                <span className="text-sm text-ink">{t(`models.check.${c.key}`)}</span>
                {c.detail && <span className="figures ml-auto text-ink-faint">{c.detail}</span>}
              </li>
            ))}
          </ul>
          <button type="button" className="text-sm text-ink-soft underline underline-offset-2 hover:text-ink">
            {t("setup.model.otherProvider")}
          </button>
        </div>
      )}

      {step === "you" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="you-name" label={t("setup.you.name")}>
            <input id="you-name" value={name} onChange={(e) => setName(e.target.value)} className={inputCls} />
          </Field>
          <Field id="you-tz" label={t("setup.you.timeZone")} hint={t("setup.you.timeZoneHint")}>
            <input id="you-tz" defaultValue={model.timeZone} className={cn(inputCls, "font-mono text-sm")} />
          </Field>
        </div>
      )}
    </WizardFrame>
  );
}
