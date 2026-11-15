import { useMutation } from "@tanstack/react-query";
import { api, errorText, must } from "@/api/client";
import { Field, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

export type Where = "local" | "remote";

/** WhereStep is the first question of the desktop app: this computer or a server (§15.3, step 1). */
export function WhereStep({
  where,
  onWhere,
  url,
  onUrl,
  error,
}: {
  where: Where;
  onWhere: (w: Where) => void;
  url: string;
  onUrl: (u: string) => void;
  error?: string | null;
}) {
  const opt = (w: Where) => (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-3 rounded-sm border px-3 py-2.5",
        where === w ? "border-ink bg-surface-raised" : "border-rule hover:border-rule-strong",
      )}
    >
      <input
        type="radio"
        name="where"
        checked={where === w}
        onChange={() => onWhere(w)}
        className="mt-1 accent-[var(--accent)]"
      />
      <span>
        <span className="block text-sm font-[var(--weight-medium)] text-ink">{t(`setup.where.${w}.title`)}</span>
        <span className="block text-sm text-ink-soft">{t(`setup.where.${w}.body`)}</span>
      </span>
    </label>
  );
  return (
    <div className="space-y-2">
      {opt("local")}
      {opt("remote")}
      {where === "remote" && (
        <div className="pt-2">
          <Field id="server-url" label={t("setup.where.url")} error={error}>
            <input
              id="server-url"
              type="url"
              value={url}
              placeholder="https://fettle.example.com"
              onChange={(e) => onUrl(e.target.value)}
              className={inputCls}
            />
          </Field>
        </div>
      )}
    </div>
  );
}

export function useWhere() {
  const local = useMutation({ mutationFn: () => must(api.POST("/api/v1/desktop/local")) });
  const remote = useMutation({
    mutationFn: (url: string) => must(api.POST("/api/v1/desktop/connect", { body: { url } })),
  });
  return { local, remote, errorText };
}
