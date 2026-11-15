import { Link } from "@tanstack/react-router";
import {
  Check,
  ChevronRight,
  CircleAlert,
  Clock,
  Lock,
  Minus,
  PenOff,
  ScrollText,
  TriangleAlert,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useAdminUsers,
  useDiagnostics,
  useEdition,
  useInstance,
  useSession,
  useSetTelemetry,
  useTelemetry,
} from "@/api/hooks";
import type { SchemaEditionView, SchemaFeatureView, SchemaLicenseView } from "@/api/schema";
import { Notice, Page, Section } from "@/app/Page";
import { Btn } from "@/components/form";
import { Switch } from "@/components/ui/switch";
import { SettingsNav } from "@/features/settings/SettingsLayout";
import { has, t, tn } from "@/lib/copy";
import { formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";

function Loading() {
  return (
    <p className="text-sm text-ink-faint" role="status">
      {t("common.loading")}
    </p>
  );
}

function Telemetry() {
  const q = useTelemetry();
  const set = useSetTelemetry();
  if (q.isPending) return <Loading />;
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

const dlRow = "grid grid-cols-[minmax(7rem,auto)_1fr] gap-x-4 border-b border-rule py-1.5";

function Instance() {
  const q = useInstance();
  if (q.isPending) return <Loading />;
  if (q.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("admin.instance.loadFailed")}>
        {errorText(q.error)}
      </Notice>
    );
  const v = q.data;
  return (
    <dl className="border-t border-rule text-sm">
      <div className={dlRow}>
        <dt className="text-ink-soft">{t("admin.instance.mode")}</dt>
        <dd className="text-ink">{t(`admin.instance.modes.${v.mode}`)}</dd>
      </div>
      <div className={dlRow}>
        <dt className="text-ink-soft">{t("admin.instance.version")}</dt>
        <dd className="font-mono text-sm break-all text-ink">{v.version}</dd>
      </div>
      <div className={dlRow}>
        <dt className="text-ink-soft">{t("admin.instance.edition")}</dt>
        <dd className="text-ink">{t(`admin.editions.${v.edition.name}`)}</dd>
      </div>
    </dl>
  );
}

function Users() {
  const q = useAdminUsers(true);
  if (q.isPending) return <Loading />;
  if (q.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("admin.users.loadFailed")}>
        {errorText(q.error)}
      </Notice>
    );
  if (q.data.length === 0)
    return <p className="border-y border-rule py-3 text-sm text-ink-faint">{t("admin.users.none")}</p>;
  return (
    <ul className="border-t border-rule" aria-label={t("admin.users.list")}>
      {q.data.map((u) => {
        const status = has(`admin.users.status.${u.status}`) ? t(`admin.users.status.${u.status}`) : u.status;
        return (
          <li key={u.id} className="grid grid-cols-[1fr_auto] items-start gap-x-4 border-b border-rule py-2">
            <div className="min-w-0">
              <p className="flex flex-wrap items-center gap-x-1.5 text-sm font-[var(--weight-medium)] text-ink">
                <span className="truncate">{u.name || u.email}</span>
                {u.is_admin && (
                  <span className="rounded-xs border border-rule px-1 text-2xs font-[var(--weight-regular)] text-ink-soft">
                    {t("admin.users.admin")}
                  </span>
                )}
              </p>
              <p className="truncate text-xs text-ink-faint">
                {u.name && `${u.email} · `}
                {tn("admin.users.spaces", u.spaces)}
              </p>
            </div>
            <div className="text-right">
              <p className={cn("text-sm", u.status === "active" ? "text-ink-soft" : "text-danger")}>{status}</p>
              <p className="figures text-ink-faint">{t("admin.users.joined", { when: formatDay(u.created_at) })}</p>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

type FeatureState = "available" | "readOnly" | "unavailable";

function featureState(f: SchemaFeatureView): FeatureState {
  if (!f.available) return "unavailable";
  return f.writable ? "available" : "readOnly";
}

const featureIcon = { available: Check, readOnly: PenOff, unavailable: Minus } as const;

function LicenceState({ l }: { l: SchemaLicenseView }) {
  const text =
    l.state === "grace" ? t("admin.licence.states.grace", { n: l.days_left }) : t(`admin.licence.states.${l.state}`);
  const detail = l.state === "invalid" && l.reason ? l.reason : null;
  if (l.state === "valid")
    return (
      <div className="mb-4">
        <p className="flex items-center gap-2 text-sm text-ink">
          <Check aria-hidden className="size-4 text-ok" />
          {text}
        </p>
        <dl className="mt-2 border-t border-rule text-sm">
          {l.customer && (
            <div className={dlRow}>
              <dt className="text-ink-soft">{t("admin.licence.customer")}</dt>
              <dd className="break-words text-ink">{l.customer}</dd>
            </div>
          )}
          {!!l.seats && (
            <div className={dlRow}>
              <dt className="text-ink-soft">{t("admin.licence.seats")}</dt>
              <dd className="figures !text-sm text-ink">{l.seats}</dd>
            </div>
          )}
          {l.expires && (
            <div className={dlRow}>
              <dt className="text-ink-soft">{t("admin.licence.expires")}</dt>
              <dd className="figures !text-sm text-ink">{l.expires}</dd>
            </div>
          )}
        </dl>
      </div>
    );
  if (l.state === "grace")
    return (
      <div
        role="alert"
        className="mb-4 flex items-start gap-2.5 rounded-sm border border-warn/50 bg-warn-wash px-3 py-2.5 text-sm text-ink"
      >
        <Clock aria-hidden className="mt-0.5 size-4 shrink-0 text-warn" />
        <p>{text}</p>
      </div>
    );
  return (
    <div className="mb-4">
      <Notice
        icon={l.state === "missing" ? TriangleAlert : CircleAlert}
        tone={l.state === "missing" ? "plain" : "danger"}
        title={text}
      >
        {detail}
      </Notice>
    </div>
  );
}

function Features({ edition }: { edition: SchemaEditionView }) {
  return (
    <ul className="border-t border-rule" aria-label={t("admin.licence.list")}>
      {(edition.features ?? []).map((f) => {
        const s = featureState(f);
        const Icon = featureIcon[s];
        const name = has(`admin.licence.features.${f.key}`) ? t(`admin.licence.features.${f.key}`) : f.key;
        return (
          <li key={f.key} className="grid grid-cols-[1fr_auto] items-center gap-x-4 border-b border-rule py-2">
            <span className="min-w-0 text-sm text-ink">{name}</span>
            <span
              className={cn(
                "inline-flex items-center gap-1.5 text-sm",
                s === "available" && "text-ok",
                s === "readOnly" && "text-warn",
                s === "unavailable" && "text-ink-faint",
              )}
            >
              <Icon aria-hidden className="size-3.5 shrink-0" />
              {t(`admin.licence.feature.${s}`)}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function Licence() {
  const q = useEdition(true);
  if (q.isPending) return <Loading />;
  if (q.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("admin.licence.loadFailed")}>
        {errorText(q.error)}
      </Notice>
    );
  const e = q.data;
  return (
    <>
      <p className="mb-3 text-sm text-ink">{t(`admin.editions.${e.name}`)}</p>
      {e.name === "community" ? (
        <p className="mb-3 text-sm text-ink-soft">{t("admin.licence.community")}</p>
      ) : (
        e.license && <LicenceState l={e.license} />
      )}
      <Features edition={e} />
    </>
  );
}

function Diagnostics() {
  const run = useDiagnostics();
  const checks = run.data ?? [];
  const passed = checks.filter((c) => c.pass).length;
  return (
    <>
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <p className="mr-auto text-sm text-ink-soft">{t("admin.diagnostics.body")}</p>
        <Btn size="sm" busy={run.isPending} onClick={() => run.mutate()}>
          {run.isSuccess ? t("admin.diagnostics.again") : t("admin.diagnostics.run")}
        </Btn>
      </div>
      {run.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("admin.diagnostics.failed")}>
          {errorText(run.error)}
        </Notice>
      )}
      {run.isSuccess && (
        <>
          <p className="mb-2 text-sm text-ink" role="status">
            {t("admin.diagnostics.summary", { pass: passed, n: checks.length })}
          </p>
          <ul className="rounded-sm border border-rule bg-surface-raised" aria-label={t("admin.diagnostics.list")}>
            {checks.map((c) => (
              <li key={c.name} className="border-b border-rule/70 px-3 py-2 last:border-b-0">
                <div className="flex items-center gap-2.5">
                  {c.pass ? (
                    <Check aria-hidden className="size-3.5 shrink-0 text-ok" />
                  ) : (
                    <X aria-hidden className="size-3.5 shrink-0 text-danger" />
                  )}
                  <span className="min-w-0 text-sm break-words text-ink">{c.name}</span>
                  <span className={cn("figures ml-auto shrink-0", c.pass ? "text-ink-faint" : "text-danger")}>
                    {c.pass ? t("admin.diagnostics.pass") : t("admin.diagnostics.fail")}
                  </span>
                </div>
                {c.reason && <p className="mt-0.5 pl-6 text-xs break-words text-ink-faint">{c.reason}</p>}
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

export function AdminPage() {
  const { me } = useSession();
  return (
    <Page title={t("shell.settings")} wide>
      <SettingsNav />
      <div className="max-w-3xl">
        {!me.is_admin ? (
          <Notice icon={Lock} title={t("admin.adminOnly")} />
        ) : (
          <>
            <Section id="instance-section" title={t("admin.instance.title")}>
              <Instance />
            </Section>
            <Section id="users-section" title={t("admin.users.title")}>
              <Users />
            </Section>
            <Section id="licence-section" title={t("admin.licence.title")}>
              <Licence />
            </Section>
            <Section id="telemetry-section" title={t("admin.title")}>
              <Telemetry />
            </Section>
            <Section id="diagnostics-section" title={t("admin.diagnostics.title")}>
              <Diagnostics />
            </Section>
            <Section id="audit-section" title={t("admin.audit.title")}>
              <Link
                to="/settings/audit"
                className="grid grid-cols-[auto_1fr_auto] items-center gap-3 border-y border-rule px-1 py-2.5 hover:bg-surface-sunk sm:px-2"
              >
                <ScrollText aria-hidden className="size-4 text-ink-faint" />
                <span className="min-w-0">
                  <span className="block text-sm font-[var(--weight-medium)] text-ink">{t("admin.audit.link")}</span>
                  <span className="block text-xs text-ink-faint">{t("admin.audit.body")}</span>
                </span>
                <ChevronRight aria-hidden className="size-4 text-ink-faint" />
              </Link>
            </Section>
          </>
        )}
      </div>
    </Page>
  );
}
