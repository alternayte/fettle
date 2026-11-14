import { useEffect, useState } from "react";
import { Plug, Plus, RotateCw, Trash2 } from "lucide-react";
import { useRouterState } from "@tanstack/react-router";
import { toast } from "sonner";
import { api, errorText, must } from "@/api/client";
import {
  useAddAccount,
  useAddComposio,
  useAuthorize,
  useCatalog,
  useConnectors,
  useCreateConnector,
  useDeleteConnector,
  useSession,
  useSetTool,
  useTestConnector,
  useZones,
  type NewConnector,
} from "@/api/hooks";
import type { SchemaConnectorView } from "@/api/schema";
import { Section } from "@/app/Page";
import { Btn, Field, SecretInput, inputCls, textareaCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import { PendingChange, type Pending } from "./PendingChange";
import { NativeSelect } from "./ProviderForm";

const CLASSES = ["read", "write_local", "write_external", "send", "admin"] as const;

type Tool = { name: string; description: string; class: string; disabled: boolean };

function ToolList({ c, account }: { c: SchemaConnectorView; account: string }) {
  const test = useTestConnector();
  const setTool = useSetTool();
  const [tools, setTools] = useState<Tool[] | null>(null);
  const load = () =>
    test.mutate(
      { id: c.id, account },
      { onSuccess: (r) => setTools(r.tools ?? []), onError: (err) => toast.error(errorText(err)) },
    );
  return (
    <div className="mt-2">
      <Btn size="sm" busy={test.isPending} onClick={load}>
        <RotateCw aria-hidden className="size-3.5" />
        {t("connectors.test")}
      </Btn>
      {tools && (
        <ul aria-label={t("connectors.tools")} className="mt-2 divide-y divide-rule border-y border-rule">
          {tools.map((tl) => (
            <li key={tl.name} className="grid grid-cols-[1fr_auto_auto] items-center gap-3 py-2">
              <div className="min-w-0">
                <p className={cn("font-mono text-sm text-ink", tl.disabled && "text-ink-faint line-through")}>
                  {tl.name}
                </p>
                <p className="truncate text-xs text-ink-faint">{tl.description}</p>
              </div>
              <NativeSelect
                id={`tool-${c.id}-${tl.name}`}
                value={tl.class}
                onChange={(cls) => {
                  setTool.mutate({ id: c.id, tool: tl.name, cls, disabled: tl.disabled });
                  setTools((ts) => ts!.map((x) => (x.name === tl.name ? { ...x, class: cls } : x)));
                }}
                className="w-44"
              >
                {CLASSES.map((x) => (
                  <option key={x} value={x}>
                    {t(`connectors.classes.${x}`)}
                  </option>
                ))}
              </NativeSelect>
              <Btn
                size="sm"
                kind="quiet"
                onClick={() => {
                  setTool.mutate({ id: c.id, tool: tl.name, cls: c.tools?.[tl.name]?.class, disabled: !tl.disabled });
                  setTools((ts) => ts!.map((x) => (x.name === tl.name ? { ...x, disabled: !x.disabled } : x)));
                }}
              >
                {tl.disabled ? t("connectors.enable") : t("connectors.disable")}
              </Btn>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ConnectorRow({ c, onPending }: { c: SchemaConnectorView; onPending: (p: Pending) => void }) {
  const { space } = useSession();
  const zones = useZones();
  const add = useAddAccount();
  const authorize = useAuthorize();
  const del = useDeleteConnector();
  const [label, setLabel] = useState("");
  const [value, setValue] = useState("");
  const [open, setOpen] = useState(false);
  const p = `conn-${c.id}`;
  const grant = async (account: string, zone: string) => {
    try {
      const r = await must(
        api.POST("/api/v1/spaces/{space}/zones/{zone}/grants", {
          params: { path: { space: space.id, zone } },
          body: { kind: "connector_account", target_id: account },
        }),
      );
      const z = (zones.data ?? []).find((x) => x.id === zone);
      onPending({
        card: r.card_id,
        title: t("connectors.grant"),
        detail: t("connectors.grantAsk"),
        rows: [
          [t("connectors.name"), c.name],
          ["zone", z?.name ?? ""],
        ],
      });
    } catch (err) {
      toast.error(t("connectors.failed"), { description: errorText(err) });
    }
  };
  return (
    <li className="border-b border-rule py-3">
      <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3">
        <Plug aria-hidden className="size-4 text-ink-faint" />
        <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="min-w-0 text-left">
          <span className="block text-sm font-[var(--weight-medium)] text-ink">{c.name}</span>
          <span className="figures block truncate text-ink-faint">
            {t(`connectors.kinds.${c.kind}`)} · {c.url || (c.command ?? []).join(" ")} · {(c.accounts ?? []).length}{" "}
            {t("connectors.accounts").toLowerCase()}
          </span>
        </button>
        <Btn
          size="sm"
          kind="quiet"
          aria-label={t("connectors.delete")}
          busy={del.isPending}
          onClick={() => del.mutate(c.id)}
        >
          <Trash2 aria-hidden className="size-3.5" />
        </Btn>
      </div>
      {open && (
        <div className="mt-3 ml-7 space-y-4">
          <div>
            <h3 className="label-caps mb-1">{t("connectors.accounts")}</h3>
            {(c.accounts ?? []).length === 0 && <p className="text-sm text-ink-faint">{t("connectors.noAccounts")}</p>}
            <ul className="space-y-3">
              {(c.accounts ?? []).map((acc) => (
                <li key={acc.id} className="rounded-sm border border-rule bg-surface px-3 py-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-[var(--weight-medium)] text-ink">{acc.label}</span>
                    <span className="text-xs text-ink-faint">
                      {(acc.zones ?? []).length
                        ? t("connectors.zones", { zones: (acc.zones ?? []).join(", ") })
                        : t("connectors.noZones")}
                    </span>
                    <NativeSelect
                      id={`${p}-${acc.id}-grant`}
                      value=""
                      onChange={(z) => z && void grant(acc.id, z)}
                      className="ml-auto w-44"
                    >
                      <option value="">{t("connectors.grant")}</option>
                      {(zones.data ?? [])
                        .filter((z) => !(acc.zones ?? []).includes(z.name))
                        .map((z) => (
                          <option key={z.id} value={z.id}>
                            {z.name}
                          </option>
                        ))}
                    </NativeSelect>
                  </div>
                  <ToolList c={c} account={acc.id} />
                </li>
              ))}
            </ul>
          </div>
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              const l = label.trim() || "default";
              if (c.auth === "oauth") {
                authorize.mutate(
                  { id: c.id, label: l },
                  { onSuccess: (r) => (window.location.href = r.url), onError: (err) => toast.error(errorText(err)) },
                );
                return;
              }
              add.mutate(
                { id: c.id, label: l, value: value || undefined },
                {
                  onSuccess: () => {
                    setLabel("");
                    setValue("");
                  },
                  onError: (err) => toast.error(t("connectors.failed"), { description: errorText(err) }),
                },
              );
            }}
          >
            <Field id={`${p}-label`} label={t("connectors.label")} className="w-48">
              <input
                id={`${p}-label`}
                value={label}
                maxLength={80}
                onChange={(e) => setLabel(e.target.value)}
                className={inputCls}
              />
            </Field>
            {c.auth === "header" && (
              <Field id={`${p}-key`} label={t("connectors.key")} className="w-64">
                <SecretInput id={`${p}-key`} value={value} onChange={setValue} />
              </Field>
            )}
            <Btn type="submit" busy={add.isPending || authorize.isPending}>
              {c.auth === "oauth" ? t("connectors.signIn") : t("connectors.addAccount")}
            </Btn>
          </form>
        </div>
      )}
    </li>
  );
}

function CustomForm({ initial, onDone }: { initial?: NewConnector; onDone: () => void }) {
  const create = useCreateConnector();
  const [name, setName] = useState(initial?.name ?? "");
  const [stdio, setStdio] = useState(false);
  const [url, setUrl] = useState(initial?.url ?? "");
  const [command, setCommand] = useState("");
  const [auth, setAuth] = useState<"none" | "oauth" | "header">(initial?.auth ?? "oauth");
  const [header, setHeader] = useState(initial?.header_name ?? "Authorization");
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        const body: NewConnector = stdio
          ? {
              name: name.trim(),
              kind: "mcp_stdio",
              command: command
                .split("\n")
                .map((x) => x.trim())
                .filter(Boolean),
              auth: "none",
            }
          : {
              name: name.trim(),
              url: url.trim(),
              auth,
              header_name: auth === "header" ? header : undefined,
              icon: initial?.icon,
            };
        create.mutate(body, { onSuccess: onDone, onError: (err) => setError(errorText(err)) });
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="conn-name" label={t("connectors.name")}>
          <input
            id="conn-name"
            value={name}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        {!initial && (
          <label className="flex items-center gap-2 self-end pb-2 text-sm text-ink">
            <input
              type="checkbox"
              checked={stdio}
              onChange={(e) => setStdio(e.target.checked)}
              className="accent-[var(--accent)]"
            />
            {t("connectors.stdio")}
          </label>
        )}
      </div>
      {stdio ? (
        <Field id="conn-command" label={t("connectors.command")} hint={t("connectors.commandHint")}>
          <textarea
            id="conn-command"
            rows={3}
            value={command}
            spellCheck={false}
            onChange={(e) => setCommand(e.target.value)}
            className={cn(textareaCls, "font-mono text-sm")}
          />
        </Field>
      ) : (
        <div className="grid gap-4 sm:grid-cols-3">
          <Field id="conn-url" label={t("connectors.url")} hint={t("connectors.urlHint")} className="sm:col-span-2">
            <input
              id="conn-url"
              value={url}
              spellCheck={false}
              inputMode="url"
              onChange={(e) => setUrl(e.target.value)}
              className={cn(inputCls, "font-mono text-sm")}
            />
          </Field>
          <Field id="conn-auth" label={t("connectors.auth")}>
            <NativeSelect id="conn-auth" value={auth} onChange={(v) => setAuth(v as "none" | "oauth" | "header")}>
              {(["oauth", "header", "none"] as const).map((x) => (
                <option key={x} value={x}>
                  {t(`connectors.auths.${x}`)}
                </option>
              ))}
            </NativeSelect>
          </Field>
          {auth === "header" && (
            <Field id="conn-header" label={t("connectors.headerName")}>
              <input
                id="conn-header"
                value={header}
                onChange={(e) => setHeader(e.target.value)}
                className={cn(inputCls, "font-mono text-sm")}
              />
            </Field>
          )}
        </div>
      )}
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onDone}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={create.isPending}>
          {t("connectors.add")}
        </Btn>
      </div>
    </form>
  );
}

export function ConnectorsPage() {
  const catalog = useCatalog();
  const connectors = useConnectors();
  const composio = useAddComposio();
  const [form, setForm] = useState<NewConnector | "custom" | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [composioKey, setComposioKey] = useState("");
  const search = useRouterState({ select: (s) => s.location.search as { connected?: string; error?: string } });
  useEffect(() => {
    if (search.connected) toast.success(t("connectors.connected"));
    if (search.error) toast.error(t("connectors.signInFailed", { reason: search.error }));
  }, [search.connected, search.error]);
  return (
    <div>
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("connectors.intro")}</p>
      {pending && (
        <div className="mb-6">
          <PendingChange
            pending={pending}
            onDone={() => {
              setPending(null);
              void connectors.refetch();
            }}
          />
        </div>
      )}
      <Section id="added" title={t("connectors.added")}>
        {(connectors.data ?? []).length === 0 ? (
          <p className="border-y border-rule py-3 text-sm text-ink-faint">{t("connectors.none")}</p>
        ) : (
          <ul aria-label={t("connectors.added")}>
            {(connectors.data ?? []).map((c) => (
              <ConnectorRow key={c.id} c={c} onPending={setPending} />
            ))}
          </ul>
        )}
      </Section>
      <Section
        id="catalog"
        title={t("connectors.catalog")}
        aside={
          form === null && (
            <Btn size="sm" onClick={() => setForm("custom")}>
              <Plus aria-hidden className="size-3.5" />
              {t("connectors.custom")}
            </Btn>
          )
        }
      >
        {form !== null && (
          <div className="mb-4 rounded-sm border border-rule bg-surface p-4">
            <CustomForm
              key={form === "custom" ? "custom" : form.name}
              initial={form === "custom" ? undefined : form}
              onDone={() => setForm(null)}
            />
          </div>
        )}
        <ul aria-label={t("connectors.catalog")} className="grid gap-x-6 sm:grid-cols-2">
          {(catalog.data ?? []).map((e) => (
            <li key={e.name} className="flex items-center gap-3 border-b border-rule py-2.5">
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-[var(--weight-medium)] text-ink">{e.name}</span>
                <span className="figures block truncate text-ink-faint">{e.url}</span>
              </span>
              <Btn
                size="sm"
                onClick={() =>
                  setForm({
                    name: e.name,
                    url: e.url,
                    auth: e.auth as "oauth",
                    header_name: e.header_name,
                    icon: e.icon,
                  })
                }
              >
                {t("connectors.connect")}
              </Btn>
            </li>
          ))}
        </ul>
      </Section>
      <Section id="composio" title={t("connectors.composio")}>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            composio.mutate(composioKey, {
              onSuccess: () => setComposioKey(""),
              onError: (err) => toast.error(t("connectors.failed"), { description: errorText(err) }),
            });
          }}
        >
          <Field
            id="composio-key"
            label={t("connectors.composioKey")}
            hint={t("connectors.composioHint")}
            className="w-80"
          >
            <SecretInput id="composio-key" value={composioKey} onChange={setComposioKey} />
          </Field>
          <Btn type="submit" busy={composio.isPending}>
            {t("connectors.composioAdd")}
          </Btn>
        </form>
      </Section>
    </div>
  );
}
