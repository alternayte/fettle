import { useEffect } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleCheck, CircleX, ExternalLink, Loader } from "lucide-react";
import { api, must } from "@/api/client";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

const DOCKER_URL = "https://www.docker.com/products/docker-desktop/";

/** ComputerStep detects the default provider and prepares it in the background (§15.3, step 2). */
export function ComputerStep() {
  const setup = useQuery({
    queryKey: ["computer-setup"],
    queryFn: () => must(api.GET("/api/v1/computer-setup")),
    refetchInterval: (q) => (q.state.data?.status.state === "running" ? 1000 : false),
  });
  const prepare = useMutation({ mutationFn: () => must(api.POST("/api/v1/computer-setup/prepare")) });
  const s = setup.data;
  const start = prepare.mutate;
  useEffect(() => {
    if (s?.default && s.status.state === "idle") start(undefined, { onSuccess: () => void setup.refetch() });
  }, [s?.default, s?.status.state, start, setup]);

  if (!s)
    return (
      <p role="status" className="text-sm text-ink-faint">
        {t("setup.computer.detecting")}
      </p>
    );
  if (!s.default)
    return (
      <div className="space-y-3">
        <p className="max-w-[var(--measure)] text-sm text-ink">{t("setup.computer.none")}</p>
        <ul className="space-y-1 text-sm text-ink-soft">
          {(s.providers ?? []).map((p) => (
            <li key={p.kind} className="flex items-start gap-2">
              <CircleX aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-faint" />
              <span>
                <span className="text-ink">{t(`setup.computer.kinds.${p.kind}`)}</span>: {p.reason}
              </span>
            </li>
          ))}
        </ul>
        <a
          href={DOCKER_URL}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1.5 text-sm text-ink underline underline-offset-2"
        >
          {t("setup.computer.getDocker")}
          <ExternalLink aria-hidden className="size-3.5" />
        </a>
        <p className="text-xs text-ink-faint">{t("setup.computer.later")}</p>
      </div>
    );
  const st = s.status;
  return (
    <div className="space-y-3">
      <p className="text-sm text-ink">{t("setup.computer.using", { name: t(`setup.computer.kinds.${s.default}`) })}</p>
      <ol aria-label={t("setup.computer.progress")} className="space-y-2 border-y border-rule py-3">
        {(st.steps ?? []).map((p) => (
          <li key={p.step} className="grid grid-cols-[1.25rem_1fr_auto] items-center gap-2 text-sm">
            {p.state === "pass" ? (
              <CircleCheck aria-label={t("setup.computer.done")} className="size-4 text-ok" />
            ) : p.state === "fail" ? (
              <CircleX aria-label={t("setup.computer.failed")} className="size-4 text-danger" />
            ) : (
              <Loader aria-label={t("setup.computer.working")} className="size-4 animate-spin text-ink-faint" />
            )}
            <span className="min-w-0 truncate text-ink">
              {p.step === "image" && p.state === "running" && p.total_mb ? (
                t("setup.computer.downloading", { mb: String(p.total_mb) })
              ) : (
                <>
                  {t(`setup.computer.steps.${p.step}`)}
                  {p.detail && <span className="text-ink-faint"> · {p.detail}</span>}
                </>
              )}
            </span>
            <span className="figures text-ink-faint">{p.state === "running" && p.percent ? `${p.percent}%` : ""}</span>
            {p.state === "running" && !!p.percent && (
              <span className="col-span-3 col-start-2 h-1 overflow-hidden rounded-full bg-surface-sunk">
                <span className="block h-full bg-glaze" style={{ width: `${p.percent}%` }} />
              </span>
            )}
          </li>
        ))}
        {(st.steps ?? []).length === 0 && <li className="text-sm text-ink-faint">{t("setup.computer.starting")}</li>}
      </ol>
      <p className={cn("text-sm", st.state === "failed" ? "text-danger" : "text-ink-soft")} role="status">
        {st.state === "ready"
          ? t("setup.computer.ready")
          : st.state === "failed"
            ? t("setup.computer.error", { error: st.error ?? "" })
            : t("setup.computer.background")}
      </p>
    </div>
  );
}
