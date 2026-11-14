import { useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ArrowRight, Check, Eye, EyeOff, Laptop, Loader, Server, X } from "lucide-react";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

export type CheckState = "idle" | "running" | "pass" | "fail";

export interface WizardModel {
  provider: { kind: string; label: string; steps: { label: string; state: CheckState; detail?: string }[] };
  modelChecks: { key: string; state: CheckState; detail?: string }[];
  timeZone: string;
}

const STEPS = ["where", "computer", "model", "you"] as const;
type Step = (typeof STEPS)[number];

function StateIcon({ s }: { s: CheckState }) {
  if (s === "pass") return <Check aria-label={t("setup.check.pass")} className="size-3.5 text-ok" />;
  if (s === "fail") return <X aria-label={t("setup.check.fail")} className="size-3.5 text-danger" />;
  if (s === "running")
    return <Loader aria-label={t("setup.check.running")} className="size-3.5 animate-spin text-info" />;
  return (
    <span aria-label={t("setup.check.idle")} className="inline-block size-3.5 rounded-full border border-rule-strong" />
  );
}

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

function Field({ id, label, children, hint }: { id: string; label: string; children: React.ReactNode; hint?: string }) {
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-sm font-[var(--weight-medium)] text-ink">
        {label}
      </label>
      {children}
      {hint && <p className="text-xs text-ink-faint">{hint}</p>}
    </div>
  );
}

const inputCls =
  "h-9 w-full rounded-sm border border-rule-strong bg-surface-raised px-2.5 text-base text-ink outline-none placeholder:text-ink-faint focus-visible:border-ink focus-visible:outline-none";

export function Wizard({ model, onFinish }: { model: WizardModel; onFinish?: () => void }) {
  const reduce = useReducedMotion();
  const [step, setStep] = useState<Step>("where");
  const [where, setWhere] = useState<"local" | "server">("local");
  const [showKey, setShowKey] = useState(false);
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const idx = STEPS.indexOf(step);
  const next = () => (idx < STEPS.length - 1 ? setStep(STEPS[idx + 1]) : onFinish?.());

  return (
    <div className="flex min-h-full items-start justify-center bg-ground px-4 py-8 sm:items-center sm:py-12">
      <div className="grid w-full max-w-3xl overflow-hidden rounded-md border border-rule-strong bg-surface shadow-[var(--shadow-2)] md:grid-cols-[13rem_1fr]">
        <aside className="border-b border-rule bg-surface-sunk/60 p-4 md:border-r md:border-b-0 md:p-5">
          <p className="serif-title text-xl text-ink">{t("app.name")}</p>
          <p className="mt-0.5 text-xs text-ink-faint">{t("setup.tagline")}</p>
          <ol className="mt-5 hidden space-y-0.5 md:block">
            {STEPS.map((s, i) => (
              <li
                key={s}
                aria-current={s === step ? "step" : undefined}
                className={cn(
                  "flex h-[var(--row)] items-center gap-2.5 text-sm",
                  i < idx ? "text-ink-soft" : i === idx ? "font-[var(--weight-strong)] text-ink" : "text-ink-faint",
                )}
              >
                <span className="figures w-4 text-right">
                  {i < idx ? <Check aria-hidden className="ml-auto size-3 text-ok" /> : i + 1}
                </span>
                {t(`setup.steps.${s}`)}
              </li>
            ))}
          </ol>
          <p className="figures mt-3 text-ink-faint md:hidden">
            {t("setup.stepOf", { n: idx + 1, total: STEPS.length })}
          </p>
        </aside>
        <main className="flex min-h-[26rem] flex-col p-5 sm:p-7">
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
                <h1 className="serif-title text-3xl text-ink">{t(`setup.${step}.title`)}</h1>
                <p className="mt-1.5 text-base text-ink-soft">{t(`setup.${step}.body`)}</p>
              </header>

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
                      <li
                        key={s.label}
                        className="flex items-center gap-2.5 border-b border-rule/70 px-3 py-2 last:border-b-0"
                      >
                        <StateIcon s={s.state} />
                        <span className="text-sm text-ink">{s.label}</span>
                        {s.detail && <span className="figures ml-auto text-ink-faint">{s.detail}</span>}
                      </li>
                    ))}
                  </ul>
                  <p className="border-t border-rule px-3 py-2 text-xs text-ink-faint">
                    {t("setup.computer.background")}
                  </p>
                </div>
              )}

              {step === "model" && (
                <div className="space-y-4">
                  <Field id="model-key" label={t("setup.model.keyLabel")} hint={t("setup.model.keyHint")}>
                    <div className="relative">
                      <input
                        id="model-key"
                        type={showKey ? "text" : "password"}
                        autoComplete="off"
                        spellCheck={false}
                        value={key}
                        onChange={(e) => setKey(e.target.value)}
                        placeholder="sk-or-…"
                        className={cn(inputCls, "pr-9 font-mono text-sm")}
                      />
                      <button
                        type="button"
                        onClick={() => setShowKey((v) => !v)}
                        aria-label={showKey ? t("vault.hide") : t("vault.show")}
                        aria-pressed={showKey}
                        className="absolute top-1/2 right-1 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded-xs text-ink-faint hover:text-ink"
                      >
                        {showKey ? <EyeOff className="size-4" aria-hidden /> : <Eye className="size-4" aria-hidden />}
                      </button>
                    </div>
                  </Field>
                  <ul className="rounded-sm border border-rule bg-surface-raised" aria-label={t("setup.model.checks")}>
                    {model.modelChecks.map((c) => (
                      <li
                        key={c.key}
                        className="flex items-center gap-2.5 border-b border-rule/70 px-3 py-2 last:border-b-0"
                      >
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
            </motion.div>
          </AnimatePresence>
          <footer className="mt-6 flex items-center gap-2 border-t border-rule pt-4">
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
          </footer>
        </main>
      </div>
    </div>
  );
}
