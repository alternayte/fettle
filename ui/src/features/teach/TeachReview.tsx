import { useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { CircleAlert, Plus, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  type TeachEdit,
  useSession,
  useTeachDiscard,
  useTeachEdit,
  useTeachRetry,
  useTeachSave,
  useTryTeach,
  useVault,
} from "@/api/hooks";
import type { SchemaTeachStepView, SchemaTeachView } from "@/api/schema";
import { Btn, Field, inputCls, textareaCls } from "@/components/form";
import { DiffView } from "@/components/DiffView";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { NativeSelect } from "@/features/settings/ProviderForm";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import { StepIcon } from "./StepIcon";
import { stepText } from "./steps";

type Input = { name: string; description: string; example?: string };
type StepState = { value?: string; command?: string; notes: string; vault_item: string; removed: boolean };

function initialSteps(steps: SchemaTeachStepView[]): Record<number, StepState> {
  const out: Record<number, StepState> = {};
  for (const s of steps)
    out[s.seq] = {
      value: s.value,
      command: s.command,
      notes: (s.notes ?? []).join("\n"),
      vault_item: s.vault_item ?? "",
      removed: s.removed,
    };
  return out;
}

/** stepEdits returns the changes of the steps against the session. */
function stepEdits(steps: SchemaTeachStepView[], st: Record<number, StepState>): NonNullable<TeachEdit["steps"]> {
  const out: NonNullable<TeachEdit["steps"]> = [];
  for (const s of steps) {
    const e = st[s.seq];
    if (!e) continue;
    const ed: NonNullable<TeachEdit["steps"]>[number] = { seq: s.seq };
    let changed = false;
    if (!s.secret && s.kind === "browser" && (e.value ?? "") !== (s.value ?? "")) {
      ed.value = e.value ?? "";
      changed = true;
    }
    if (s.kind === "command" && (e.command ?? "") !== (s.command ?? "")) {
      ed.command = e.command ?? "";
      changed = true;
    }
    const notes = e.notes
      .split("\n")
      .map((n) => n.trim())
      .filter(Boolean);
    if (notes.join("\n") !== (s.notes ?? []).join("\n")) {
      ed.notes = notes;
      changed = true;
    }
    if (e.vault_item !== (s.vault_item ?? "")) {
      ed.vault_item = e.vault_item;
      changed = true;
    }
    if (e.removed !== s.removed) {
      ed.removed = e.removed;
      changed = true;
    }
    if (changed) out.push(ed);
  }
  return out;
}

function Shot({ session, seq }: { session: string; seq: number }) {
  const { space } = useSession();
  const [open, setOpen] = useState(false);
  const src = `/api/v1/spaces/${space.id}/teach/${session}/steps/${seq}/screenshot`;
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="block w-28 shrink-0 overflow-hidden rounded-xs border border-rule bg-surface-sunk"
        aria-label={t("teach.review.shot", { n: seq })}
      >
        <img src={src} alt="" loading="lazy" className="aspect-[16/10] w-full object-cover object-top" />
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-[min(64rem,calc(100%-2rem))] border-rule-strong bg-surface-raised p-3 sm:max-w-[min(64rem,calc(100%-2rem))]">
          <DialogTitle className="text-sm text-ink">{t("teach.review.shot", { n: seq })}</DialogTitle>
          <img src={src} alt={t("teach.review.shot", { n: seq })} className="w-full rounded-xs border border-rule" />
        </DialogContent>
      </Dialog>
    </>
  );
}

function StepEditor({
  session,
  s,
  n,
  st,
  set,
  items,
  readOnly,
  loginFirst,
}: {
  session: string;
  loginFirst: boolean;
  s: SchemaTeachStepView;
  n: number;
  st: StepState;
  set: (v: Partial<StepState>) => void;
  items: { name: string; kind: string }[];
  readOnly: boolean;
}) {
  const id = `step-${s.seq}`;
  const terminal = s.kind === "command";
  const choices = items.filter((it) => (terminal ? it.kind !== "totp" : it.kind === "login"));
  return (
    <li className={cn("flex gap-3 border-b border-rule px-3 py-3 last:border-b-0", st.removed && "opacity-50")}>
      <span className="figures w-5 shrink-0 pt-0.5 text-right text-xs text-ink-faint">{n}</span>
      {s.screenshot && <Shot session={session} seq={s.seq} />}
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex items-start gap-2">
          <StepIcon s={s} />
          <div className="min-w-0 flex-1">
            <p className="label-caps text-ink-faint">
              {s.login
                ? t("teach.review.login", { n: s.login })
                : t(`teach.kind.${s.kind === "browser" ? s.action : s.kind}`)}
            </p>
            <p className={cn("break-words text-sm text-ink", terminal && "font-mono text-xs")}>
              {terminal ? s.command : stepText(s)}
            </p>
            {s.kind === "browser" && s.url && s.action !== "navigate" && (
              <p className="truncate text-xs text-ink-faint">{s.url}</p>
            )}
            {terminal && (
              <p className="text-xs text-ink-faint">
                {t("teach.step.exit", {
                  cwd: !s.cwd || s.cwd === "." ? t("teach.step.home") : s.cwd,
                  code: s.exit_code,
                })}
              </p>
            )}
          </div>
          {!readOnly && (
            <Btn
              size="sm"
              kind="quiet"
              aria-label={st.removed ? t("teach.review.restore", { n }) : t("teach.review.remove", { n })}
              onClick={() => set({ removed: !st.removed })}
            >
              {st.removed ? (
                <RotateCcw aria-hidden className="size-3.5" />
              ) : (
                <Trash2 aria-hidden className="size-3.5" />
              )}
            </Btn>
          )}
        </div>
        {!readOnly &&
          !st.removed &&
          s.kind === "browser" &&
          !s.secret &&
          (s.action === "type" || s.action === "select") && (
            <input
              aria-label={t("teach.review.value", { n })}
              value={st.value ?? ""}
              maxLength={2000}
              onChange={(e) => set({ value: e.target.value })}
              className={cn(inputCls, "h-8 text-sm")}
            />
          )}
        {!readOnly && !st.removed && terminal && (
          <input
            aria-label={t("teach.review.command", { n })}
            value={st.command ?? ""}
            maxLength={4000}
            onChange={(e) => set({ command: e.target.value })}
            className={cn(inputCls, "h-8 font-mono text-xs")}
          />
        )}
        {terminal && s.output && (
          <pre className="max-h-24 overflow-auto rounded-xs bg-surface-sunk p-1.5 font-mono text-[11px] text-ink-soft">
            {s.output}
          </pre>
        )}
        {loginFirst && !st.removed && (
          <div className="flex flex-wrap items-center gap-2">
            <label htmlFor={`${id}-vault`} className="text-xs text-ink-soft">
              {terminal ? t("teach.review.vaultSecret") : t("teach.review.vaultLogin")}
            </label>
            <NativeSelect
              id={`${id}-vault`}
              value={st.vault_item}
              onChange={(v) => set({ vault_item: v })}
              className="min-w-48"
            >
              <option value="">{t("teach.review.chooseItem")}</option>
              {choices.map((it) => (
                <option key={it.name} value={it.name}>
                  {it.name}
                </option>
              ))}
            </NativeSelect>
            {choices.length === 0 && (
              <Link to="/settings/vault" className="text-xs text-ink underline underline-offset-2">
                {t("teach.review.addItem")}
              </Link>
            )}
          </div>
        )}
        {(!readOnly || st.notes) && !st.removed && (
          <textarea
            aria-label={t("teach.review.notes", { n })}
            rows={Math.max(1, st.notes.split("\n").length)}
            value={st.notes}
            readOnly={readOnly}
            placeholder={t("teach.review.notesPlaceholder")}
            onChange={(e) => set({ notes: e.target.value })}
            className={cn(textareaCls, "text-xs")}
          />
        )}
      </div>
    </li>
  );
}

function TryIt({ session }: { session: SchemaTeachView }) {
  const tryIt = useTryTeach(session.id);
  const navigate = useNavigate();
  const inputs = session.draft?.inputs ?? [];
  const [values, setValues] = useState<Record<string, string>>({});
  return (
    <form
      className="space-y-3 rounded-sm border border-glaze/50 bg-surface p-4"
      onSubmit={(e) => {
        e.preventDefault();
        tryIt.mutate(values, {
          onSuccess: (r) =>
            void navigate({ to: "/chat/$chatId", params: { chatId: r.chat_id }, search: { run: r.run_id } }),
          onError: (err) => toast.error(t("teach.tryFailed"), { description: errorText(err) }),
        });
      }}
    >
      <h2 className="text-sm font-[var(--weight-strong)] text-ink">{t("teach.tryTitle")}</h2>
      <p className="text-sm text-ink-soft">{t("teach.tryBody")}</p>
      {inputs.map((in_) => (
        <Field key={in_.name} id={`try-${in_.name}`} label={in_.name} hint={in_.description}>
          <input
            id={`try-${in_.name}`}
            value={values[in_.name] ?? ""}
            placeholder={in_.example}
            onChange={(e) => setValues((v) => ({ ...v, [in_.name]: e.target.value }))}
            className={inputCls}
          />
        </Field>
      ))}
      <Btn kind="act" type="submit" busy={tryIt.isPending}>
        {t("teach.try")}
      </Btn>
    </form>
  );
}

/** TeachReview is the review card of a taught skill: SKILL.md, the inputs, the steps and the evals. */
export function TeachReview({ session }: { session: SchemaTeachView }) {
  const draft = session.draft;
  const vault = useVault();
  const save = useTeachSave(session.id);
  const edit = useTeachEdit(session.id);
  const discard = useTeachDiscard(session.id);
  const retry = useTeachRetry(session.id);
  const [md, setMd] = useState(draft?.skill_md ?? "");
  const [inputs, setInputs] = useState<Input[]>(() => (draft?.inputs ?? []).map((i) => ({ ...i })) ?? []);
  const [evals, setEvals] = useState(draft?.evals_json ?? "");
  const [steps, setSteps] = useState(() => initialSteps(session.steps ?? []));
  const review = session.status === "review";
  const readOnly = !review;
  const items = useMemo(() => (vault.data ?? []).map((it) => ({ name: it.name, kind: it.kind })), [vault.data]);
  // The vault item of a login goes on its first step.
  const loginFirst = useMemo(() => {
    const seen = new Set<number>();
    const first = new Set<number>();
    for (const s of session.steps ?? [])
      if (s.login && !s.removed && !seen.has(s.login)) {
        seen.add(s.login);
        first.add(s.seq);
      }
    return first;
  }, [session.steps]);

  const body = (): TeachEdit => ({
    skill_md: md,
    inputs,
    evals_json: evals,
    steps: stepEdits(session.steps ?? [], steps),
  });

  if (session.status === "failed")
    return (
      <div className="mx-auto w-full max-w-2xl space-y-3 p-6">
        <div
          role="alert"
          className="flex items-start gap-2 rounded-sm border border-danger/40 bg-surface px-4 py-3 text-sm"
        >
          <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-danger" />
          <p>{session.error}</p>
        </div>
        <div className="flex gap-2">
          <Btn
            kind="act"
            busy={retry.isPending}
            onClick={() => retry.mutate(undefined, { onError: (e) => toast.error(errorText(e)) })}
          >
            {t("teach.retry")}
          </Btn>
          <Btn kind="quiet" busy={discard.isPending} onClick={() => discard.mutate(undefined)}>
            {t("teach.discard")}
          </Btn>
        </div>
      </div>
    );
  if (session.status === "discarded")
    return (
      <p className="mx-auto w-full max-w-2xl p-6 text-sm text-ink-soft">{session.error || t("teach.discarded")}</p>
    );

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-4xl space-y-8 px-4 py-6">
        {session.status === "saved" && (
          <div className="space-y-4">
            <p role="status" className="text-sm text-ink">
              {t("teach.saved", { name: draft?.name ?? "", version: session.skill_version ?? 0 })}{" "}
              {session.skill_id && (
                <Link
                  to="/skills/$skillId"
                  params={{ skillId: session.skill_id }}
                  className="underline underline-offset-2"
                >
                  {t("teach.openSkill")}
                </Link>
              )}
            </p>
            <TryIt session={session} />
          </div>
        )}
        {review && (
          <p className="max-w-[var(--measure)] text-sm text-ink-soft">
            {draft?.base_skill_id ? t("teach.review.introPatch", { name: draft.name }) : t("teach.review.intro")}
          </p>
        )}
        {draft?.diff && (
          <section aria-labelledby="teach-diff" className="space-y-2">
            <h2 id="teach-diff" className="label-caps text-ink-faint">
              {t("teach.review.diff", { name: draft.name })}
            </h2>
            <DiffView diff={draft.diff} label={t("teach.review.diff", { name: draft.name })} />
          </section>
        )}
        <section aria-labelledby="teach-skill" className="space-y-2">
          <h2 id="teach-skill" className="label-caps text-ink-faint">
            SKILL.md
          </h2>
          <textarea
            aria-labelledby="teach-skill"
            rows={18}
            value={md}
            readOnly={readOnly}
            spellCheck={false}
            onChange={(e) => setMd(e.target.value)}
            className={cn(textareaCls, "font-mono text-xs leading-relaxed")}
          />
        </section>
        <section aria-labelledby="teach-inputs" className="space-y-2">
          <h2 id="teach-inputs" className="label-caps text-ink-faint">
            {t("teach.review.inputs")}
          </h2>
          {inputs.length === 0 && <p className="text-sm text-ink-faint">{t("teach.review.noInputs")}</p>}
          <ul className="space-y-2">
            {inputs.map((in_, i) => (
              <li key={i} className="grid grid-cols-1 gap-2 sm:grid-cols-[10rem_1fr_10rem_auto]">
                {(["name", "description", "example"] as const).map((k) => (
                  <input
                    key={k}
                    aria-label={t(`teach.review.input.${k}`, { n: i + 1 })}
                    placeholder={t(`teach.review.input.${k}Placeholder`)}
                    value={in_[k] ?? ""}
                    readOnly={readOnly}
                    onChange={(e) => setInputs((l) => l.map((x, j) => (j === i ? { ...x, [k]: e.target.value } : x)))}
                    className={cn(inputCls, "h-8 text-sm", k === "name" && "font-mono")}
                  />
                ))}
                {!readOnly && (
                  <Btn
                    size="sm"
                    kind="quiet"
                    aria-label={t("teach.review.removeInput", { n: i + 1 })}
                    onClick={() => setInputs((l) => l.filter((_, j) => j !== i))}
                  >
                    <Trash2 aria-hidden className="size-3.5" />
                  </Btn>
                )}
              </li>
            ))}
          </ul>
          {!readOnly && (
            <Btn size="sm" onClick={() => setInputs((l) => [...l, { name: "", description: "", example: "" }])}>
              <Plus aria-hidden className="size-3.5" />
              {t("teach.review.addInput")}
            </Btn>
          )}
        </section>
        <section aria-labelledby="teach-steps" className="space-y-2">
          <h2 id="teach-steps" className="label-caps text-ink-faint">
            {t("teach.review.steps")}
          </h2>
          <p className="text-xs text-ink-faint">{t("teach.review.stepsHint")}</p>
          <ol className="rounded-sm border border-rule bg-surface">
            {(session.steps ?? []).map((s, i) => (
              <StepEditor
                key={s.seq}
                session={session.id}
                s={s}
                n={i + 1}
                st={steps[s.seq] ?? { notes: "", vault_item: "", removed: false }}
                set={(v) => setSteps((m) => ({ ...m, [s.seq]: { ...m[s.seq], ...v } }))}
                items={items}
                readOnly={readOnly}
                loginFirst={loginFirst.has(s.seq)}
              />
            ))}
          </ol>
        </section>
        <section aria-labelledby="teach-evals" className="space-y-2">
          <h2 id="teach-evals" className="label-caps text-ink-faint">
            evals.json
          </h2>
          <p className="text-xs text-ink-faint">{t("teach.review.evalsHint")}</p>
          <textarea
            aria-labelledby="teach-evals"
            rows={10}
            value={evals}
            readOnly={readOnly}
            spellCheck={false}
            onChange={(e) => setEvals(e.target.value)}
            className={cn(textareaCls, "font-mono text-xs")}
          />
        </section>
        {review && (
          <div className="sticky bottom-0 -mx-4 flex flex-wrap items-center gap-2 border-t border-rule-strong bg-ground/95 px-4 py-3 backdrop-blur">
            {session.can_save ? (
              <Btn
                kind="act"
                busy={save.isPending}
                onClick={() =>
                  save.mutate(body(), {
                    onSuccess: (r) =>
                      toast.success(t("teach.saved", { name: r.draft?.name ?? "", version: r.skill_version ?? 0 })),
                    onError: (err) => toast.error(t("teach.saveFailed"), { description: errorText(err) }),
                  })
                }
              >
                {t("teach.save")}
              </Btn>
            ) : (
              <p className="text-sm text-ink-soft">{t("teach.ownerSaves")}</p>
            )}
            <Btn
              busy={edit.isPending}
              onClick={() =>
                edit.mutate(body(), {
                  onSuccess: () => toast.success(t("teach.changesKept")),
                  onError: (err) => toast.error(errorText(err)),
                })
              }
            >
              {t("teach.keepChanges")}
            </Btn>
            <Btn kind="quiet" className="ml-auto" busy={discard.isPending} onClick={() => discard.mutate(undefined)}>
              {t("teach.discard")}
            </Btn>
          </div>
        )}
      </div>
    </div>
  );
}
