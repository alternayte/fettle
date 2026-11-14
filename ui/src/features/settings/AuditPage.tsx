import { useEffect, useMemo, useState } from "react";
import { CircleAlert, ScrollText } from "lucide-react";
import { errorText } from "@/api/client";
import { useAudit, useSession } from "@/api/hooks";
import { Notice, Section } from "@/app/Page";
import { inputCls } from "@/components/form";
import { has, t } from "@/lib/copy";
import { formatClock, formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";

export function AuditPage() {
  const { me } = useSession();
  const [text, setText] = useState("");
  const [action, setAction] = useState("");
  useEffect(() => {
    const id = setTimeout(() => setAction(text.trim()), 300);
    return () => clearTimeout(id);
  }, [text]);
  const audit = useAudit(action);
  const all = useAudit("");
  const actions = useMemo(() => [...new Set((all.data ?? []).map((e) => e.action))].sort(), [all.data]);
  const [open, setOpen] = useState<number | null>(null);

  const actor = (kind: string, id: string) => {
    if (kind === "user" && id === me.id) return t("chat.you");
    const k = has(`audit.actor.${kind}`) ? t(`audit.actor.${kind}`) : kind;
    return id ? `${k} ${id.slice(0, 8)}` : k;
  };

  return (
    <div className="max-w-4xl">
      <Section id="audit" title={t("audit.title")}>
        <p className="mb-3 text-sm text-ink-soft">{t("audit.body")}</p>
        <div className="mb-4 max-w-sm space-y-1">
          <label htmlFor="audit-action" className="block text-sm font-[var(--weight-medium)] text-ink">
            {t("audit.filter")}
          </label>
          <input
            id="audit-action"
            type="search"
            list="audit-actions"
            value={text}
            spellCheck={false}
            placeholder={t("audit.placeholder")}
            onChange={(e) => setText(e.target.value)}
            className={cn(inputCls, "font-mono text-sm")}
          />
          <datalist id="audit-actions">
            {actions.map((a) => (
              <option key={a} value={a} />
            ))}
          </datalist>
        </div>
        {audit.isError && (
          <Notice icon={CircleAlert} tone="danger" title={t("audit.loadFailed")}>
            {errorText(audit.error)}
          </Notice>
        )}
        {audit.isPending && (
          <p className="text-sm text-ink-faint" role="status">
            {t("common.loading")}
          </p>
        )}
        {audit.isSuccess && audit.data.length === 0 && <Notice icon={ScrollText} title={t("audit.empty")} />}
        {audit.isSuccess && audit.data.length > 0 && (
          <ol className={cn("border-t border-rule", audit.isFetching && "opacity-70")}>
            {audit.data.map((e, i) => {
              const details = Object.entries((e.details ?? {}) as Record<string, unknown>);
              return (
                <li key={`${e.at}-${i}`} className="border-b border-rule py-2">
                  <div className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5 sm:grid-cols-[9rem_1fr_auto]">
                    <time
                      dateTime={e.at}
                      className="figures order-2 text-right text-ink-faint sm:order-none sm:text-left"
                    >
                      {formatDay(e.at)} {formatClock(e.at)}
                    </time>
                    <div className="order-1 min-w-0 sm:order-none">
                      <p className="font-mono text-sm break-all text-ink">{e.action}</p>
                      <p className="text-xs break-all text-ink-faint">
                        {actor(e.actor_kind, e.actor_id)} · <span className="font-mono">{e.target}</span>
                      </p>
                    </div>
                    {details.length > 0 && (
                      <button
                        type="button"
                        aria-expanded={open === i}
                        onClick={() => setOpen((o) => (o === i ? null : i))}
                        className="order-3 col-span-2 justify-self-start text-xs text-ink-soft underline underline-offset-2 hover:text-ink sm:col-span-1 sm:justify-self-end"
                      >
                        {open === i ? t("audit.hideDetails") : t("audit.details", { n: details.length })}
                      </button>
                    )}
                  </div>
                  {open === i && (
                    <dl className="mt-2 grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-1 rounded-xs border border-rule bg-surface-sunk p-2 text-xs sm:ml-[9.75rem]">
                      {details.map(([k, v]) => (
                        <div key={k} className="contents">
                          <dt className="figures text-ink-faint">{k}</dt>
                          <dd className="min-w-0 font-mono break-all text-ink-soft">
                            {typeof v === "string" ? v : JSON.stringify(v)}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  )}
                </li>
              );
            })}
          </ol>
        )}
      </Section>
    </div>
  );
}
