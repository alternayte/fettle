import { useState } from "react";
import { Ban, Check, CircleHelp, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useAddRule, useDeleteRule, usePolicy, useSetMode } from "@/api/hooks";
import type { SchemaRuleView } from "@/api/schema";
import { Section } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import { PendingChange, type Pending } from "./PendingChange";
import { NativeSelect } from "./ProviderForm";

const MODES = ["ask", "auto", "never"] as const;
type Mode = (typeof MODES)[number];
type Effect = "allow" | "ask" | "block";
type Target = "tool" | "site" | "recipient";

const effectIcon = { allow: Check, ask: CircleHelp, block: Ban } as const;

function RuleForm({ onDone }: { onDone: () => void }) {
  const add = useAddRule();
  const [effect, setEffect] = useState<Effect>("ask");
  const [target, setTarget] = useState<Target>("tool");
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        const v = value.trim();
        if (!v) return setError(t("approvals.needValue"));
        const body = {
          effect,
          ...(target === "tool" ? { tool: v } : target === "site" ? { site_pattern: v } : { recipient_pattern: v }),
        };
        add.mutate(body, {
          onSuccess: () => {
            toast.success(t("approvals.ruleAdded"));
            onDone();
          },
          onError: (err) => setError(errorText(err)),
        });
      }}
    >
      <div className="grid gap-4 sm:grid-cols-[9rem_10rem_1fr]">
        <Field id="rule-effect" label={t("approvals.effect")}>
          <NativeSelect id="rule-effect" value={effect} onChange={(v) => setEffect(v as Effect)}>
            {(["allow", "ask", "block"] as const).map((x) => (
              <option key={x} value={x}>
                {t(`approvals.effects.${x}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id="rule-target" label={t("approvals.matches")}>
          <NativeSelect id="rule-target" value={target} onChange={(v) => setTarget(v as Target)}>
            {(["tool", "site", "recipient"] as const).map((x) => (
              <option key={x} value={x}>
                {t(`approvals.targets.${x}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id="rule-value" label={t(`approvals.valueLabel.${target}`)} hint={t(`approvals.valueHint.${target}`)}>
          <input
            id="rule-value"
            value={value}
            spellCheck={false}
            aria-describedby="rule-value-hint"
            onChange={(e) => setValue(e.target.value)}
            className={cn(inputCls, "font-mono text-sm")}
          />
        </Field>
      </div>
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onDone}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={add.isPending}>
          {t("approvals.addRule")}
        </Btn>
      </div>
    </form>
  );
}

function RuleRow({ rule }: { rule: SchemaRuleView }) {
  const del = useDeleteRule();
  const Icon = effectIcon[rule.effect as Effect] ?? CircleHelp;
  const what = rule.tool
    ? t("approvals.forTool", { tool: rule.tool === "*" ? t("approvals.everyTool") : rule.tool })
    : rule.site_pattern
      ? t("approvals.forSite", { site: rule.site_pattern })
      : t("approvals.forRecipient", { who: rule.recipient_pattern });
  return (
    <li className="grid grid-cols-[6rem_1fr_auto] items-center gap-3 border-b border-rule py-2.5">
      <span
        className={cn(
          "inline-flex items-center gap-1.5 text-sm font-[var(--weight-medium)]",
          rule.effect === "block" ? "text-danger" : "text-ink",
        )}
      >
        <Icon aria-hidden className="size-3.5" />
        {t(`approvals.effects.${rule.effect}`)}
      </span>
      <span className="min-w-0 truncate text-sm text-ink">
        {what}
        {rule.from_card && <span className="ml-2 text-xs text-ink-faint">{t("approvals.fromCard")}</span>}
      </span>
      <Btn
        kind="quiet"
        size="sm"
        busy={del.isPending}
        aria-label={t("approvals.deleteRule", { what })}
        onClick={() => del.mutate(rule.id, { onError: (err) => toast.error(errorText(err)) })}
      >
        <Trash2 aria-hidden className="size-3.5" />
      </Btn>
    </li>
  );
}

export function ApprovalsPage() {
  const policy = usePolicy();
  const setMode = useSetMode();
  const [pending, setPending] = useState<Pending | null>(null);
  const [adding, setAdding] = useState(false);
  const mode = (policy.data?.mode ?? "ask") as Mode;
  return (
    <div>
      {pending && (
        <div className="mb-6">
          <PendingChange
            pending={pending}
            onDone={(allowed) => {
              setPending(null);
              void policy.refetch();
              toast(allowed ? t("change.allowed") : t("change.denied"));
            }}
          />
        </div>
      )}
      <Section id="mode" title={t("approvals.mode")}>
        <fieldset>
          <legend className="sr-only">{t("approvals.mode")}</legend>
          <div className="divide-y divide-rule border-y border-rule">
            {MODES.map((m) => (
              <label
                key={m}
                className={cn("flex cursor-pointer items-start gap-3 px-1 py-3", m === mode && "bg-surface")}
              >
                <input
                  type="radio"
                  name="approval-mode"
                  value={m}
                  checked={m === mode}
                  disabled={setMode.isPending || pending !== null}
                  onChange={() =>
                    setMode.mutate(m, {
                      onSuccess: (r) =>
                        setPending({
                          card: r.card_id,
                          title: t("approvals.changeTitle"),
                          detail: t(`approvals.modeHint.${m}`),
                          rows: [[t("approvals.newMode"), t(`approvals.modes.${m}`)]],
                        }),
                      onError: (err) => toast.error(errorText(err)),
                    })
                  }
                  className="mt-1 accent-[var(--accent)]"
                />
                <span>
                  <span className="text-sm font-[var(--weight-medium)] text-ink">{t(`approvals.modes.${m}`)}</span>
                  {m === mode && <span className="label-caps ml-2">{t("approvals.current")}</span>}
                  <span className="block text-sm text-ink-soft">{t(`approvals.modeHint.${m}`)}</span>
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        <p className="mt-2 text-xs text-ink-faint">{t("approvals.hardAsks")}</p>
      </Section>
      <Section
        id="rules"
        title={t("approvals.rules")}
        aside={
          !adding && (
            <Btn size="sm" onClick={() => setAdding(true)}>
              <Plus aria-hidden className="size-3.5" />
              {t("approvals.newRule")}
            </Btn>
          )
        }
      >
        <p className="mb-3 max-w-[var(--measure)] text-sm text-ink-soft">{t("approvals.rulesIntro")}</p>
        {adding && (
          <div className="mb-3 rounded-sm border border-rule bg-surface p-4">
            <RuleForm onDone={() => setAdding(false)} />
          </div>
        )}
        {(policy.data?.rules ?? []).length === 0 && !adding ? (
          <p className="border-y border-rule py-3 text-sm text-ink-faint">{t("approvals.noRules")}</p>
        ) : (
          <ul aria-label={t("approvals.rules")}>
            {(policy.data?.rules ?? []).map((r) => (
              <RuleRow key={r.id} rule={r} />
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}
