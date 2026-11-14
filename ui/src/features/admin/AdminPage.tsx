import { CircleAlert, Lock } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useSession, useSetTelemetry, useTelemetry } from "@/api/hooks";
import { Notice, Page, Section } from "@/app/Page";
import { Switch } from "@/components/ui/switch";
import { SettingsNav } from "@/features/settings/SettingsLayout";
import { t } from "@/lib/copy";

function Telemetry() {
  const q = useTelemetry();
  const set = useSetTelemetry();
  if (q.isPending)
    return (
      <p className="text-sm text-ink-faint" role="status">
        {t("common.loading")}
      </p>
    );
  if (q.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("admin.loadFailed")}>
        {errorText(q.error)}
      </Notice>
    );
  const v = q.data;
  const preview = v.preview as unknown as Record<string, unknown>;
  return (
    <div className="space-y-4">
      <div className="flex items-start gap-3 rounded-sm border border-rule bg-surface px-3 py-3">
        <Switch
          id="telemetry"
          checked={v.enabled}
          disabled={set.isPending}
          onCheckedChange={(on) =>
            set.mutate(on, {
              onSuccess: () => toast.success(on ? t("admin.on") : t("admin.off")),
              onError: (err) => toast.error(t("admin.saveFailed"), { description: errorText(err) }),
            })
          }
          className="mt-0.5"
        />
        <div className="min-w-0">
          <label htmlFor="telemetry" className="text-sm font-[var(--weight-medium)] text-ink">
            {t("admin.toggle")}
          </label>
          <p className="text-xs text-ink-faint">
            {v.enabled ? t("admin.stateOn") : t("admin.stateOff")}
            {v.endpoint && (
              <>
                {" "}
                {t("admin.endpoint")} <span className="font-mono break-all">{v.endpoint}</span>
              </>
            )}
          </p>
        </div>
      </div>
      <div>
        <h3 className="mb-1 text-sm font-[var(--weight-strong)] text-ink">{t("admin.fields")}</h3>
        <p className="mb-2 text-xs text-ink-faint">{t("admin.fieldsBody")}</p>
        <dl className="border-t border-rule">
          {(v.fields ?? []).map((f) => (
            <div key={f} className="grid grid-cols-[minmax(8rem,auto)_1fr] gap-x-4 border-b border-rule py-1.5">
              <dt className="font-mono text-xs text-ink-soft">{f}</dt>
              <dd className="figures !text-sm break-all text-ink">{String(preview[f] ?? "")}</dd>
            </div>
          ))}
        </dl>
      </div>
    </div>
  );
}

export function AdminPage() {
  const { me } = useSession();
  return (
    <Page title={t("shell.settings")} wide>
      <SettingsNav />
      <div className="max-w-3xl">
        <Section id="telemetry-section" title={t("admin.title")}>
          {me.is_admin ? <Telemetry /> : <Notice icon={Lock} title={t("admin.adminOnly")} />}
        </Section>
      </div>
    </Page>
  );
}
