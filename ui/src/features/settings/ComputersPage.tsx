import { useState } from "react";
import { Monitor, Plus } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useComputerProviders, useCreateZone, useEnableHost, useUpdateZone, useZones } from "@/api/hooks";
import type { SchemaComputerProviderView, SchemaZoneBody, SchemaZoneView } from "@/api/schema";
import { Notice, Section } from "@/app/Page";
import { Btn, Field, inputCls, textareaCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import { PendingChange, type Pending } from "./PendingChange";
import { NativeSelect } from "./ProviderForm";

const SIZES = ["small", "browser", "desktop"] as const;
type Size = (typeof SIZES)[number];

const stateTone: Record<string, string> = {
  running: "bg-ok",
  starting: "bg-info",
  sleeping: "bg-ink-faint",
  stopping: "bg-ink-faint",
  error: "bg-danger",
  quarantined: "bg-danger",
  none: "border border-rule-strong",
};

function ComputerState({ zone }: { zone: SchemaZoneView }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-ink-soft">
      <span
        aria-hidden
        className={cn("inline-block size-2 rounded-full", stateTone[zone.computer_state] ?? "bg-ink-faint")}
      />
      {t(`computers.state.${zone.computer_state}`)}
    </span>
  );
}

function ZoneForm({
  zone,
  providers,
  busy,
  onSubmit,
  onCancel,
}: {
  zone?: SchemaZoneView;
  providers: SchemaComputerProviderView[];
  busy: boolean;
  onSubmit: (body: SchemaZoneBody) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(zone?.name ?? "");
  const [providerID, setProviderID] = useState(zone?.provider_id ?? "");
  const [network, setNetwork] = useState<"open" | "allowlist">((zone?.network as "open" | "allowlist") ?? "open");
  const [hosts, setHosts] = useState((zone?.allow_hosts ?? []).join("\n"));
  const [size, setSize] = useState<Size>((zone?.size as Size) ?? "browser");
  const [idle, setIdle] = useState(String(zone?.idle_minutes ?? 10));
  const [error, setError] = useState<string | null>(null);
  const p = zone ? `zone-${zone.id}` : "zone-new";
  const usable = providers.filter((pr) => pr.enabled && pr.available);
  const chosen = usable.find((pr) => pr.id === providerID) ?? usable.find((pr) => pr.default);
  const noAllowlist = network === "allowlist" && chosen !== undefined && !chosen.caps.network_allowlist;
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        if (!name.trim()) return setError(t("computers.needName"));
        const allow = hosts
          .split(/[\s,]+/)
          .map((h) => h.trim())
          .filter(Boolean);
        if (network === "allowlist" && allow.length === 0) return setError(t("computers.needHosts"));
        if (noAllowlist) return setError(t("computers.noAllowlist"));
        const minutes = Number(idle);
        if (!Number.isInteger(minutes) || minutes < 1 || minutes > 1440) return setError(t("computers.badIdle"));
        onSubmit({
          name: name.trim(),
          provider_id: providerID || undefined,
          network,
          allow_hosts: network === "allowlist" ? allow : [],
          size,
          idle_minutes: minutes,
        });
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={`${p}-name`} label={t("computers.name")}>
          <input
            id={`${p}-name`}
            value={name}
            maxLength={60}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field id={`${p}-provider`} label={t("computers.provider")}>
          <NativeSelect id={`${p}-provider`} value={providerID} onChange={setProviderID}>
            <option value="">{t("computers.defaultProvider")}</option>
            {usable.map((pr) => (
              <option key={pr.id} value={pr.id}>
                {t(`computers.kind.${pr.kind}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id={`${p}-size`} label={t("computers.size")} hint={t(`computers.sizeHint.${size}`)}>
          <NativeSelect
            id={`${p}-size`}
            value={size}
            onChange={(v) => setSize(v as Size)}
            describedBy={`${p}-size-hint`}
          >
            {SIZES.map((s) => (
              <option key={s} value={s}>
                {t(`computers.sizes.${s}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id={`${p}-idle`} label={t("computers.idle")} hint={t("computers.idleHint")}>
          <input
            id={`${p}-idle`}
            inputMode="numeric"
            value={idle}
            aria-describedby={`${p}-idle-hint`}
            onChange={(e) => setIdle(e.target.value)}
            className={cn(inputCls, "figures w-28")}
          />
        </Field>
      </div>
      <fieldset className="space-y-2">
        <legend className="text-sm font-[var(--weight-medium)] text-ink">{t("computers.network")}</legend>
        {(["open", "allowlist"] as const).map((n) => (
          <label key={n} className="flex items-start gap-2 text-sm text-ink">
            <input
              type="radio"
              name={`${p}-network`}
              value={n}
              checked={network === n}
              onChange={() => setNetwork(n)}
              className="mt-1 accent-[var(--accent)]"
            />
            <span>
              {t(`computers.networks.${n}`)}
              <span className="block text-xs text-ink-faint">{t(`computers.networkHint.${n}`)}</span>
            </span>
          </label>
        ))}
        {network === "allowlist" && (
          <Field
            id={`${p}-hosts`}
            label={t("computers.hosts")}
            hint={t("computers.hostsHint")}
            error={noAllowlist ? t("computers.noAllowlist") : null}
          >
            <textarea
              id={`${p}-hosts`}
              rows={3}
              value={hosts}
              spellCheck={false}
              onChange={(e) => setHosts(e.target.value)}
              className={cn(textareaCls, "font-mono text-sm")}
            />
          </Field>
        )}
      </fieldset>
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onCancel}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={busy}>
          {zone ? t("common.save") : t("computers.ask")}
        </Btn>
      </div>
    </form>
  );
}

function ZoneRow({ zone, providers }: { zone: SchemaZoneView; providers: SchemaComputerProviderView[] }) {
  const [editing, setEditing] = useState(false);
  const update = useUpdateZone();
  const network =
    zone.network === "open"
      ? t("computers.networks.open")
      : t("computers.allowCount", { n: String(zone.allow_hosts?.length ?? 0) });
  return (
    <li className="border-b border-rule py-3">
      <div className="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 sm:grid-cols-[minmax(8rem,1fr)_8rem_14rem_auto]">
        <div className="min-w-0">
          <p className="truncate text-sm font-[var(--weight-medium)] text-ink">{zone.name}</p>
          <p className="truncate text-xs text-ink-faint">
            {zone.bots?.length ? zone.bots.join(", ") : t("computers.noBots")}
          </p>
        </div>
        <ComputerState zone={zone} />
        <p className="figures order-3 col-span-2 text-ink-soft sm:order-none sm:col-span-1">
          {[t(`computers.kind.${zone.provider_kind}`), t(`computers.sizes.${zone.size}`), network].join(" · ")}
        </p>
        <Btn
          kind="quiet"
          size="sm"
          aria-expanded={editing}
          className="justify-self-end"
          onClick={() => setEditing((e) => !e)}
        >
          {editing ? t("common.close") : t("common.edit")}
        </Btn>
      </div>
      {zone.computer_error && zone.computer_state === "error" && (
        <p className="mt-1 text-xs text-danger">{zone.computer_error}</p>
      )}
      {editing && (
        <div className="mt-3 rounded-sm border border-rule bg-surface p-4">
          <ZoneForm
            zone={zone}
            providers={providers}
            busy={update.isPending}
            onCancel={() => setEditing(false)}
            onSubmit={(body) =>
              update.mutate(
                { id: zone.id, body },
                {
                  onSuccess: () => {
                    setEditing(false);
                    toast.success(t("computers.saved"));
                  },
                  onError: (err) => toast.error(t("computers.saveFailed"), { description: errorText(err) }),
                },
              )
            }
          />
        </div>
      )}
    </li>
  );
}

function ProviderRow({ p, onPending }: { p: SchemaComputerProviderView; onPending: (x: Pending) => void }) {
  const enable = useEnableHost();
  const caps = [
    p.caps.network_allowlist && t("computers.caps.allowlist"),
    p.caps.snapshot && t("computers.caps.snapshot"),
    p.caps.native_fork && t("computers.caps.fork"),
    p.caps.screen && t("computers.caps.screen"),
  ].filter(Boolean) as string[];
  return (
    <li className="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 border-b border-rule py-3 sm:grid-cols-[minmax(8rem,1fr)_minmax(10rem,1.4fr)_8rem]">
      <div className="min-w-0">
        <p className="text-sm font-[var(--weight-medium)] text-ink">
          {t(`computers.kind.${p.kind}`)}
          {p.default && <span className="label-caps ml-2">{t("computers.default")}</span>}
        </p>
        <p className="text-xs text-ink-faint">{p.available ? t("computers.available") : t("computers.unavailable")}</p>
      </div>
      <p className="order-3 col-span-2 text-xs text-ink-soft sm:order-none sm:col-span-1">
        {caps.length ? caps.join(" · ") : t("computers.caps.none")}
      </p>
      <div className="justify-self-end">
        {p.enabled ? (
          <span className="inline-flex items-center gap-1.5 text-sm text-ink-soft">
            <span aria-hidden className="inline-block size-2 rounded-full bg-ok" />
            {t("computers.on")}
          </span>
        ) : p.kind === "host" && p.available ? (
          <Btn
            size="sm"
            busy={enable.isPending}
            onClick={() =>
              enable.mutate(undefined, {
                onSuccess: (r) =>
                  onPending({
                    card: r.card_id,
                    title: t("computers.hostTitle"),
                    detail: t("computers.hostDetail"),
                    rows: [],
                  }),
                onError: (err) => toast.error(t("computers.saveFailed"), { description: errorText(err) }),
              })
            }
          >
            {t("computers.turnOn")}
          </Btn>
        ) : (
          <span className="text-sm text-ink-faint">{t("computers.off")}</span>
        )}
      </div>
    </li>
  );
}

export function ComputersPage() {
  const zones = useZones();
  const providers = useComputerProviders();
  const create = useCreateZone();
  const [adding, setAdding] = useState(false);
  const [pending, setPending] = useState<Pending | null>(null);
  const provs = providers.data ?? [];
  const done = (allowed: boolean) => {
    setPending(null);
    void zones.refetch();
    void providers.refetch();
    toast(allowed ? t("change.allowed") : t("change.denied"));
  };
  return (
    <div>
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("computers.intro")}</p>
      {pending && (
        <div className="mb-6">
          <PendingChange pending={pending} onDone={done} />
        </div>
      )}
      <Section
        id="zones"
        title={t("computers.zones")}
        aside={
          !adding && (
            <Btn size="sm" onClick={() => setAdding(true)}>
              <Plus aria-hidden className="size-3.5" />
              {t("computers.newZone")}
            </Btn>
          )
        }
      >
        {adding && (
          <div className="mb-3 rounded-sm border border-rule bg-surface p-4">
            <ZoneForm
              providers={provs}
              busy={create.isPending}
              onCancel={() => setAdding(false)}
              onSubmit={(body) =>
                create.mutate(body, {
                  onSuccess: (r) => {
                    setAdding(false);
                    const rows: [string, string][] = [
                      [t("computers.name"), body.name],
                      [t("computers.size"), t(`computers.sizes.${body.size}`)],
                      [
                        t("computers.network"),
                        body.network === "open" ? t("computers.networks.open") : (body.allow_hosts ?? []).join(", "),
                      ],
                    ];
                    setPending({
                      card: r.card_id,
                      title: t("computers.createTitle"),
                      detail: t("computers.createDetail"),
                      rows,
                    });
                  },
                  onError: (err) => toast.error(t("computers.saveFailed"), { description: errorText(err) }),
                })
              }
            />
          </div>
        )}
        {zones.isError ? (
          <Notice icon={Monitor} title={t("computers.loadFailed")} tone="danger">
            {errorText(zones.error)}
          </Notice>
        ) : (
          <ul aria-label={t("computers.zones")}>
            {(zones.data ?? []).map((z) => (
              <ZoneRow key={z.id} zone={z} providers={provs} />
            ))}
          </ul>
        )}
      </Section>
      <Section id="providers" title={t("computers.providers")}>
        <ul aria-label={t("computers.providers")}>
          {provs.map((p) => (
            <ProviderRow key={p.id} p={p} onPending={setPending} />
          ))}
        </ul>
      </Section>
    </div>
  );
}
