import { KeyRound } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useAnswerChange } from "@/api/hooks";
import { Btn } from "@/components/form";
import { t } from "@/lib/copy";

export type Pending = { card: string; title: string; detail: string; rows: [string, string][] };

/** PendingChange shows a hard-ask change in place, with the same choice as its card in the primary bot's chat. */
export function PendingChange({ pending, onDone }: { pending: Pending; onDone: (allowed: boolean) => void }) {
  const answer = useAnswerChange();
  const decide = (action: "allow" | "deny") =>
    answer.mutate(
      { card: pending.card, action },
      {
        onSuccess: () => onDone(action === "allow"),
        onError: (err) => toast.error(t("card.answerFailed"), { description: errorText(err) }),
      },
    );
  return (
    <section aria-label={pending.title} className="rounded-sm border border-glaze/50 bg-glaze-wash px-4 py-3">
      <div className="flex items-center gap-2">
        <KeyRound aria-hidden className="size-4 text-glaze" />
        <h3 className="text-sm font-[var(--weight-strong)] text-ink">{pending.title}</h3>
        <span className="label-caps ml-auto">{t("change.waits")}</span>
      </div>
      <p className="mt-1.5 text-sm text-ink-soft">{pending.detail}</p>
      {pending.rows.length > 0 && (
        <dl className="mt-2 grid grid-cols-[minmax(5rem,auto)_1fr] gap-x-3 gap-y-1 text-sm">
          {pending.rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="figures pt-0.5 text-ink-faint">{k}</dt>
              <dd className="min-w-0 break-words text-ink">{v}</dd>
            </div>
          ))}
        </dl>
      )}
      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <Btn kind="act" size="sm" busy={answer.isPending} onClick={() => decide("allow")}>
          {t("card.change.allow")}
        </Btn>
        <Btn kind="quiet" size="sm" disabled={answer.isPending} onClick={() => decide("deny")}>
          {t("card.change.deny")}
        </Btn>
        <span className="text-xs text-ink-faint">{t("change.alsoInChat")}</span>
      </div>
    </section>
  );
}
