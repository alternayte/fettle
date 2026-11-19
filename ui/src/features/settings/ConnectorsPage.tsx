import { useEffect, useState } from "react";
import { Pencil, Plug, Plus, RotateCw, Trash2, X } from "lucide-react";
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
  useUpdateAccount,
  useUpdateConnector,
  useZones,
  type AccountEdit,
  type ConnectorEdit,
  type ConnectorVariable,
  type NewConnector,
} from "@/api/hooks";
import type { SchemaAccountView, SchemaConnectorView } from "@/api/schema";
import { Section } from "@/app/Page";
import { Btn, Field, SecretInput, inputCls, textareaCls } from "@/components/form";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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

// ---------------------------------------------------------------- variables

type PlainRow = { name: string; value: string; declared?: ConnectorVariable };

function plainRows(declared: ConnectorVariable[], env: Record<string, string>): PlainRow[] {
  const rows: PlainRow[] = declared
    .filter((v) => !v.secret)
    .map((v) => ({ name: v.name, value: env[v.name] ?? "", declared: v }));
  for (const [k, v] of Object.entries(env)) {
    if (!rows.some((r) => r.name === k)) rows.push({ name: k, value: v });
  }
  return rows;
}

function plainEnv(rows: PlainRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) if (r.name.trim() && r.value !== "") out[r.name.trim()] = r.value;
  return out;
}

/** PlainVars edits the plain variables of a stdio connector. A declared variable keeps its name. */
function PlainVars({ id, rows, onChange }: { id: string; rows: PlainRow[]; onChange: (rows: PlainRow[]) => void }) {
  const put = (i: number, r: Partial<PlainRow>) => onChange(rows.map((x, j) => (j === i ? { ...x, ...r } : x)));
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-[var(--weight-medium)] text-ink">{t("connectors.variables")}</legend>
      <p className="text-xs text-ink-faint">{t("connectors.variablesHint")}</p>
      {rows.map((r, i) => (
        <div
          key={i}
          className="grid grid-cols-[minmax(0,1fr)_auto] sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto] items-start gap-2"
        >
          {r.declared ? (
            <span className="col-span-2 pt-2 font-mono text-sm break-all text-ink sm:col-span-1">
              {r.name}
              {r.declared.required && <span className="ml-1.5 text-xs text-ink-faint">{t("connectors.required")}</span>}
            </span>
          ) : (
            <input
              aria-label={t("connectors.varName")}
              value={r.name}
              spellCheck={false}
              onChange={(e) => put(i, { name: e.target.value })}
              className={cn(inputCls, "col-span-2 font-mono text-sm sm:col-span-1")}
            />
          )}
          <input
            id={`${id}-${i}`}
            aria-label={t("connectors.varValue", { name: r.name || t("connectors.newVariable") })}
            value={r.value}
            spellCheck={false}
            onChange={(e) => put(i, { value: e.target.value })}
            className={cn(inputCls, "font-mono text-sm")}
          />
          {r.declared ? (
            <span />
          ) : (
            <Btn
              size="sm"
              kind="quiet"
              className="mt-1"
              aria-label={t("connectors.removeVariable", { name: r.name || t("connectors.newVariable") })}
              onClick={() => onChange(rows.filter((_, j) => j !== i))}
            >
              <X aria-hidden className="size-3.5" />
            </Btn>
          )}
          {r.declared?.doc && (
            <p className="col-span-2 text-xs text-ink-faint sm:col-span-1 sm:col-start-2">{r.declared.doc}</p>
          )}
        </div>
      ))}
      <Btn size="sm" onClick={() => onChange([...rows, { name: "", value: "" }])}>
        <Plus aria-hidden className="size-3.5" />
        {t("connectors.addVariable")}
      </Btn>
    </fieldset>
  );
}

type SecretRow = {
  name: string;
  value: string;
  set: boolean;
  remove: boolean;
  declared?: ConnectorVariable;
  fresh?: boolean;
};

function secretRows(declared: ConnectorVariable[], set: string[]): SecretRow[] {
  const rows: SecretRow[] = declared
    .filter((v) => v.secret)
    .map((v) => ({ name: v.name, value: "", set: set.includes(v.name), remove: false, declared: v }));
  for (const n of set) {
    if (!rows.some((r) => r.name === n)) rows.push({ name: n, value: "", set: true, remove: false });
  }
  return rows;
}

/** secretEnv returns the changes of secret rows: a new value, or "" to remove a set variable. */
function secretEnv(rows: SecretRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    const name = r.name.trim();
    if (!name) continue;
    if (r.remove && r.set) out[name] = "";
    else if (r.value !== "") out[name] = r.value;
  }
  return out;
}

/** SecretVars edits secret variables. A set value never comes back: the row shows the name and "set". */
function SecretVars({ id, rows, onChange }: { id: string; rows: SecretRow[]; onChange: (rows: SecretRow[]) => void }) {
  const put = (i: number, r: Partial<SecretRow>) => onChange(rows.map((x, j) => (j === i ? { ...x, ...r } : x)));
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-[var(--weight-medium)] text-ink">{t("connectors.secretVariables")}</legend>
      <p className="text-xs text-ink-faint">{t("connectors.secretHint")}</p>
      {rows.map((r, i) => {
        const status = r.set
          ? r.remove
            ? t("connectors.willRemove")
            : t("connectors.set")
          : r.declared?.required
            ? t("connectors.required")
            : t("connectors.notSet");
        return (
          <div
            key={i}
            className="grid grid-cols-[minmax(0,1fr)_auto] sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto] items-start gap-2"
          >
            {r.fresh ? (
              <input
                aria-label={t("connectors.varName")}
                value={r.name}
                spellCheck={false}
                onChange={(e) => put(i, { name: e.target.value })}
                className={cn(inputCls, "col-span-2 font-mono text-sm sm:col-span-1")}
              />
            ) : (
              <label
                htmlFor={`${id}-${i}`}
                className="col-span-2 pt-2 font-mono text-sm break-all text-ink sm:col-span-1"
              >
                {r.name}
                <span className={cn("ml-1.5 text-xs text-ink-faint", r.remove && "text-danger")}>{status}</span>
              </label>
            )}
            <div>
              {r.fresh && (
                <label htmlFor={`${id}-${i}`} className="sr-only">
                  {t("connectors.varValue", { name: r.name || t("connectors.newVariable") })}
                </label>
              )}
              <SecretInput
                id={`${id}-${i}`}
                value={r.value}
                disabled={r.remove}
                onChange={(v) => put(i, { value: v })}
                placeholder={r.set ? "••••••••" : undefined}
                describedBy={r.set ? `${id}-${i}-hint` : undefined}
              />
              {r.set && (
                <p id={`${id}-${i}-hint`} className="mt-1 text-xs text-ink-faint">
                  {t("connectors.replaceHint")}
                </p>
              )}
              {r.declared?.doc && <p className="mt-1 text-xs text-ink-faint">{r.declared.doc}</p>}
            </div>
            {r.set ? (
              <Btn size="sm" kind="quiet" className="mt-1" onClick={() => put(i, { remove: !r.remove, value: "" })}>
                {r.remove ? t("connectors.keep") : t("connectors.remove")}
              </Btn>
            ) : r.fresh ? (
              <Btn
                size="sm"
                kind="quiet"
                className="mt-1"
                aria-label={t("connectors.removeVariable", { name: r.name || t("connectors.newVariable") })}
                onClick={() => onChange(rows.filter((_, j) => j !== i))}
              >
                <X aria-hidden className="size-3.5" />
              </Btn>
            ) : (
              <span />
            )}
          </div>
        );
      })}
      <Btn
        size="sm"
        onClick={() => onChange([...rows, { name: "", value: "", set: false, remove: false, fresh: true }])}
      >
        <Plus aria-hidden className="size-3.5" />
        {t("connectors.addVariable")}
      </Btn>
    </fieldset>
  );
}

// ---------------------------------------------------------------- accounts

function AccountEditor({ c, acc, onDone }: { c: SchemaConnectorView; acc: SchemaAccountView; onDone: () => void }) {
  const update = useUpdateAccount();
  const p = `acc-${acc.id}`;
  const [label, setLabel] = useState(acc.label);
  const [key, setKey] = useState("");
  const [rows, setRows] = useState<SecretRow[]>(() => secretRows(c.variables ?? [], acc.secret_env ?? []));
  return (
    <form
      noValidate
      aria-label={t("connectors.editAccount", { label: acc.label })}
      className="mt-2 space-y-3 border-t border-rule pt-3"
      onSubmit={(e) => {
        e.preventDefault();
        const body: AccountEdit = {};
        if (label.trim() && label.trim() !== acc.label) body.label = label.trim();
        if (c.auth === "header" && key) body.value = key;
        if (c.kind === "mcp_stdio") {
          const env = secretEnv(rows);
          if (Object.keys(env).length) body.secret_env = env;
        }
        if (Object.keys(body).length === 0) return onDone();
        update.mutate(
          { id: c.id, account: acc.id, body },
          {
            onSuccess: () => {
              toast.success(t("connectors.accountSaved"));
              onDone();
            },
            onError: (err) => toast.error(t("connectors.failed"), { description: errorText(err) }),
          },
        );
      }}
    >
      <Field id={`${p}-label`} label={t("connectors.label")} className="w-64">
        <input
          id={`${p}-label`}
          value={label}
          maxLength={80}
          onChange={(e) => setLabel(e.target.value)}
          className={inputCls}
        />
      </Field>
      {c.auth === "header" && (
        <Field id={`${p}-key`} label={t("connectors.newKey")} hint={t("connectors.newKeyHint")} className="w-80">
          <SecretInput id={`${p}-key`} value={key} onChange={setKey} placeholder="••••••••" />
        </Field>
      )}
      {c.kind === "mcp_stdio" && <SecretVars id={`${p}-secret`} rows={rows} onChange={setRows} />}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onDone}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={update.isPending}>
          {t("connectors.save")}
        </Btn>
      </div>
    </form>
  );
}

function AccountItem({
  c,
  acc,
  onGrant,
}: {
  c: SchemaConnectorView;
  acc: SchemaAccountView;
  onGrant: (account: string, zone: string) => void;
}) {
  const zones = useZones();
  const authorize = useAuthorize();
  const [editing, setEditing] = useState(false);
  const signInAgain = () =>
    authorize.mutate(
      { id: c.id, label: acc.label, account: acc.id },
      { onSuccess: (r) => (window.location.href = r.url), onError: (err) => toast.error(errorText(err)) },
    );
  return (
    <li className="rounded-sm border border-rule bg-surface px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-[var(--weight-medium)] text-ink">{acc.label}</span>
        <span className="text-xs text-ink-faint">
          {(acc.zones ?? []).length
            ? t("connectors.zones", { zones: (acc.zones ?? []).join(", ") })
            : t("connectors.noZones")}
        </span>
        <Btn
          size="sm"
          kind="quiet"
          aria-label={t("connectors.editAccount", { label: acc.label })}
          aria-expanded={editing}
          onClick={() => setEditing((x) => !x)}
        >
          <Pencil aria-hidden className="size-3.5" />
        </Btn>
        <NativeSelect
          id={`conn-${c.id}-${acc.id}-grant`}
          value=""
          onChange={(z) => z && onGrant(acc.id, z)}
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
      {(c.auth === "header" || (acc.secret_env ?? []).length > 0) && (
        <p className="mt-1 font-mono text-xs text-ink-faint">
          {c.auth === "header"
            ? t("connectors.keySet")
            : (acc.secret_env ?? []).map((n) => `${n}: ${t("connectors.set")}`).join(" · ")}
        </p>
      )}
      {acc.needs_sign_in && (
        <div
          role="status"
          className="mt-2 flex flex-wrap items-center gap-2 rounded-sm border border-warn/40 bg-warn-wash px-2.5 py-1.5"
        >
          <p className="text-sm text-ink">{t("connectors.needsSignIn")}</p>
          <Btn size="sm" kind="act" className="ml-auto" busy={authorize.isPending} onClick={signInAgain}>
            {t("connectors.signInAgain")}
          </Btn>
        </div>
      )}
      {editing ? (
        <AccountEditor c={c} acc={acc} onDone={() => setEditing(false)} />
      ) : (
        !acc.needs_sign_in && <ToolList c={c} account={acc.id} />
      )}
    </li>
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
  const [secrets, setSecrets] = useState<SecretRow[]>(() => secretRows(c.variables ?? [], []));
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
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
      <div className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-3">
        <Plug aria-hidden className="size-4 text-ink-faint" />
        <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="min-w-0 text-left">
          <span className="block text-sm font-[var(--weight-medium)] text-ink">{c.name}</span>
          <span className="figures block truncate text-ink-faint">
            {t(`connectors.kinds.${c.kind}`)} · {c.url || (c.command ?? []).join(" ")} · {(c.accounts ?? []).length}{" "}
            {t("connectors.accounts").toLowerCase()}
          </span>
        </button>
        {c.kind !== "composio" ? (
          <Btn
            size="sm"
            kind="quiet"
            aria-label={t("connectors.edit")}
            aria-expanded={editing}
            onClick={() => {
              setEditing((x) => !x);
              setOpen(true);
            }}
          >
            <Pencil aria-hidden className="size-3.5" />
          </Btn>
        ) : (
          <span />
        )}
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
          {editing && (
            <div className="rounded-sm border border-rule bg-surface p-4">
              <ConnectorForm edit={c} onDone={() => setEditing(false)} />
            </div>
          )}
          <div>
            <h3 className="label-caps mb-1">{t("connectors.accounts")}</h3>
            {(c.accounts ?? []).length === 0 && <p className="text-sm text-ink-faint">{t("connectors.noAccounts")}</p>}
            <ul className="space-y-3">
              {(c.accounts ?? []).map((acc) => (
                <AccountItem key={acc.id} c={c} acc={acc} onGrant={(a, z) => void grant(a, z)} />
              ))}
            </ul>
          </div>
          <form
            noValidate
            className="space-y-3"
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
              const env = c.kind === "mcp_stdio" ? secretEnv(secrets) : {};
              add.mutate(
                {
                  id: c.id,
                  label: l,
                  value: value || undefined,
                  secret_env: Object.keys(env).length ? env : undefined,
                },
                {
                  onSuccess: () => {
                    setLabel("");
                    setValue("");
                    setSecrets(secretRows(c.variables ?? [], []));
                  },
                  onError: (err) => toast.error(t("connectors.failed"), { description: errorText(err) }),
                },
              );
            }}
          >
            {c.kind === "mcp_stdio" && <SecretVars id={`${p}-new-secret`} rows={secrets} onChange={setSecrets} />}
            <div className="flex flex-wrap items-end gap-2">
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
            </div>
          </form>
        </div>
      )}
    </li>
  );
}

// ---------------------------------------------------------------- connector form

function lines(s: string): string[] {
  return s
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean);
}

/** commandText shows a command so that commandArgs reads it back unchanged. */
function commandText(cmd: string[]): string {
  if (cmd.length === 1 && /[\s'"\\]/.test(cmd[0])) return `'${cmd[0].split("'").join(`'\\''`)}'`;
  return cmd.join("\n");
}

/**
 * commandArgs reads the command field. One argument per line keeps each line as it is. A
 * single line splits into words like a shell does, with quotes and backslashes, because
 * people paste a command as one line and no program name holds a space.
 */
function commandArgs(s: string): string[] {
  const ls = lines(s);
  if (ls.length !== 1) return ls;
  const out: string[] = [];
  let cur = "";
  let started = false;
  let quote: "'" | '"' | null = null;
  const line = ls[0];
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quote === "'") {
      if (ch === "'") quote = null;
      else cur += ch;
    } else if (quote === '"') {
      if (ch === '"') quote = null;
      else if (ch === "\\" && i + 1 < line.length && '"\\$`'.includes(line[i + 1])) cur += line[++i];
      else cur += ch;
    } else if (ch === "'" || ch === '"') {
      quote = ch;
      started = true;
    } else if (ch === "\\" && i + 1 < line.length) {
      cur += line[++i];
      started = true;
    } else if (/\s/.test(ch)) {
      if (started) out.push(cur);
      cur = "";
      started = false;
    } else {
      cur += ch;
      started = true;
    }
  }
  if (started) out.push(cur);
  return out;
}

function sameEnv(a: Record<string, string>, b: Record<string, string>): boolean {
  const ka = Object.keys(a);
  return ka.length === Object.keys(b).length && ka.every((k) => a[k] === b[k]);
}

/**
 * ConnectorForm adds a connector, or edits one with `edit`. The kind and the sign-in stay
 * fixed in an edit. A catalogue entry fills `initial` and names the variables to ask for.
 */
function ConnectorForm({
  initial,
  edit,
  onDone,
}: {
  initial?: NewConnector;
  edit?: SchemaConnectorView;
  onDone: () => void;
}) {
  const create = useCreateConnector();
  const update = useUpdateConnector();
  const addAccount = useAddAccount();
  const declared = edit?.variables ?? initial?.variables ?? [];
  const f = edit ? `edit-${edit.id}` : "conn";
  const [name, setName] = useState(edit?.name ?? initial?.name ?? "");
  const [stdio, setStdio] = useState((edit?.kind ?? initial?.kind) === "mcp_stdio");
  const [url, setUrl] = useState(edit?.url ?? initial?.url ?? "");
  const [command, setCommand] = useState(commandText(edit?.command ?? initial?.command ?? []));
  const [auth, setAuth] = useState<"none" | "oauth" | "header">(
    (edit?.auth as "none" | "oauth" | "header" | undefined) ?? initial?.auth ?? "oauth",
  );
  const [header, setHeader] = useState(edit?.header_name || initial?.header_name || "Authorization");
  const [env, setEnv] = useState<PlainRow[]>(() => plainRows(declared, edit?.env ?? initial?.env ?? {}));
  const [label, setLabel] = useState("");
  const [secrets, setSecrets] = useState<SecretRow[]>(() => secretRows(declared, []));
  const [warn, setWarn] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const firstAccount = !edit && stdio && declared.some((v) => v.secret);
  const busy = create.isPending || update.isPending || addAccount.isPending;

  const saveEdit = (c: SchemaConnectorView) => {
    const body: ConnectorEdit = {};
    if (name.trim() !== c.name) body.name = name.trim();
    if (c.kind === "mcp_stdio") {
      const cmd = commandArgs(command);
      if (cmd.join("\n") !== (c.command ?? []).join("\n")) body.command = cmd;
      const e = plainEnv(env);
      if (!sameEnv(e, c.env ?? {})) body.env = e;
    } else {
      if (url.trim() !== (c.url ?? "")) body.url = url.trim();
      if (c.auth === "header" && header.trim() !== (c.header_name ?? "")) body.header_name = header.trim();
    }
    if (Object.keys(body).length === 0) return onDone();
    update.mutate(
      { id: c.id, body },
      {
        onSuccess: () => {
          toast.success(t("connectors.saved"));
          onDone();
        },
        onError: (err) => setError(errorText(err)),
      },
    );
  };

  const saveNew = () => {
    const body: NewConnector = stdio
      ? {
          name: name.trim(),
          kind: "mcp_stdio",
          command: commandArgs(command),
          auth: "none",
          env: plainEnv(env),
          variables: declared.length ? declared : undefined,
          icon: initial?.icon,
        }
      : {
          name: name.trim(),
          url: url.trim(),
          auth,
          header_name: auth === "header" ? header : undefined,
          icon: initial?.icon,
        };
    create.mutate(body, {
      onSuccess: (c) => {
        const se = secretEnv(secrets);
        if (!firstAccount || Object.keys(se).length === 0) return onDone();
        addAccount.mutate(
          { id: c.id, label: label.trim() || "default", secret_env: se },
          { onSuccess: onDone, onError: (err) => setError(errorText(err)) },
        );
      },
      onError: (err) => setError(errorText(err)),
    });
  };

  return (
    <form
      noValidate
      aria-label={edit ? t("connectors.editConnector", { name: edit.name }) : undefined}
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        if (!edit) return saveNew();
        if (edit.auth === "oauth" && url.trim() !== (edit.url ?? "")) return setWarn(true);
        saveEdit(edit);
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={`${f}-name`} label={t("connectors.name")}>
          <input
            id={`${f}-name`}
            value={name}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        {!initial && !edit && (
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
        <>
          <Field id={`${f}-command`} label={t("connectors.command")} hint={t("connectors.commandHint")}>
            <textarea
              id={`${f}-command`}
              rows={3}
              value={command}
              spellCheck={false}
              onChange={(e) => setCommand(e.target.value)}
              className={cn(textareaCls, "font-mono text-sm")}
            />
          </Field>
          <PlainVars id={`${f}-env`} rows={env} onChange={setEnv} />
          {firstAccount && (
            <div className="space-y-3 border-t border-rule pt-3">
              <h3 className="label-caps">{t("connectors.firstAccount")}</h3>
              <p className="text-xs text-ink-faint">{t("connectors.firstAccountHint")}</p>
              <Field id={`${f}-label`} label={t("connectors.label")} className="w-64">
                <input
                  id={`${f}-label`}
                  value={label}
                  maxLength={80}
                  onChange={(e) => setLabel(e.target.value)}
                  className={inputCls}
                />
              </Field>
              <SecretVars id={`${f}-secret`} rows={secrets} onChange={setSecrets} />
            </div>
          )}
        </>
      ) : (
        <div className="grid gap-4 sm:grid-cols-3">
          <Field id={`${f}-url`} label={t("connectors.url")} hint={t("connectors.urlHint")} className="sm:col-span-2">
            <input
              id={`${f}-url`}
              value={url}
              spellCheck={false}
              inputMode="url"
              onChange={(e) => setUrl(e.target.value)}
              className={cn(inputCls, "font-mono text-sm")}
            />
          </Field>
          <Field id={`${f}-auth`} label={t("connectors.auth")}>
            <NativeSelect
              id={`${f}-auth`}
              value={auth}
              disabled={!!edit}
              onChange={(v) => setAuth(v as "none" | "oauth" | "header")}
            >
              {(["oauth", "header", "none"] as const).map((x) => (
                <option key={x} value={x}>
                  {t(`connectors.auths.${x}`)}
                </option>
              ))}
            </NativeSelect>
          </Field>
          {auth === "header" && (
            <Field id={`${f}-header`} label={t("connectors.headerName")}>
              <input
                id={`${f}-header`}
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
        <Btn type="submit" kind="primary" busy={busy}>
          {edit ? t("connectors.save") : t("connectors.add")}
        </Btn>
      </div>
      {edit && (
        <Dialog open={warn} onOpenChange={setWarn}>
          <DialogContent className="border-rule-strong bg-surface-raised text-ink">
            <DialogHeader>
              <DialogTitle className="text-base">{t("connectors.urlWarnTitle")}</DialogTitle>
              <DialogDescription className="text-sm text-ink-soft">{t("connectors.urlWarnBody")}</DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Btn kind="quiet" onClick={() => setWarn(false)}>
                {t("common.cancel")}
              </Btn>
              <Btn
                kind="primary"
                busy={update.isPending}
                onClick={() => {
                  setWarn(false);
                  saveEdit(edit);
                }}
              >
                {t("connectors.urlWarnSave")}
              </Btn>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
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
            <ConnectorForm
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
                <span className="figures block truncate text-ink-faint">
                  {e.kind === "mcp_stdio" ? (e.command ?? []).join(" ") : e.url}
                </span>
              </span>
              <Btn
                size="sm"
                onClick={() =>
                  setForm({
                    name: e.name,
                    kind: e.kind,
                    url: e.url,
                    command: e.command ?? undefined,
                    auth: e.auth,
                    header_name: e.header_name,
                    icon: e.icon,
                    variables: e.variables ?? undefined,
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
