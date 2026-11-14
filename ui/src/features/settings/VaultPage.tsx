import { useState } from "react";
import { CircleAlert, KeyRound, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useDeleteVaultItem, useSaveVaultItem, useVault } from "@/api/hooks";
import type { SchemaVaultItemView } from "@/api/schema";
import { Notice, Section } from "@/app/Page";
import { Btn, Field, SecretInput, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";
import { NativeSelect } from "./ProviderForm";

type Kind = "login" | "secret" | "totp";
const KINDS: Kind[] = ["login", "secret", "totp"];

function VaultForm({
  item,
  onDone,
  onCancel,
}: {
  item?: SchemaVaultItemView;
  onDone: (saved: SchemaVaultItemView) => void;
  onCancel: () => void;
}) {
  const save = useSaveVaultItem();
  const [kind, setKind] = useState<Kind>((item?.kind as Kind) ?? "login");
  const [name, setName] = useState(item?.name ?? "");
  const [site, setSite] = useState(item?.site ?? "");
  const [username, setUsername] = useState(item?.username ?? "");
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const p = item ? `vault-${item.id}` : "vault-new";
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        if (!name.trim()) return setError(t("vault.needName"));
        if (!item && !value) return setError(t("vault.needValue"));
        save.mutate(
          {
            id: item?.id,
            body: {
              kind,
              name: name.trim(),
              site: site.trim(),
              username: kind === "login" ? username.trim() : "",
              value: value || undefined,
            },
          },
          {
            onSuccess: (v) => {
              setValue("");
              onDone(v);
            },
            onError: (err) => setError(errorText(err)),
          },
        );
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={`${p}-kind`} label={t("vault.kind")}>
          <NativeSelect id={`${p}-kind`} value={kind} onChange={(v) => setKind(v as Kind)}>
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {t(`vault.kinds.${k}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field id={`${p}-name`} label={t("vault.name")}>
          <input
            id={`${p}-name`}
            value={name}
            maxLength={120}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field id={`${p}-site`} label={t("vault.site")} hint={t("vault.siteHint")}>
          <input
            id={`${p}-site`}
            value={site}
            spellCheck={false}
            inputMode="url"
            aria-describedby={`${p}-site-hint`}
            onChange={(e) => setSite(e.target.value)}
            className={cn(inputCls, "font-mono text-sm")}
          />
        </Field>
        {kind === "login" && (
          <Field id={`${p}-user`} label={t("vault.username")}>
            <input
              id={`${p}-user`}
              value={username}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setUsername(e.target.value)}
              className={inputCls}
            />
          </Field>
        )}
      </div>
      <Field
        id={`${p}-value`}
        label={t(`vault.valueLabel.${kind}`)}
        hint={item ? t("vault.valueKeep", { last4: item.last4 }) : t("vault.valueHint")}
      >
        <SecretInput
          id={`${p}-value`}
          value={value}
          onChange={setValue}
          describedBy={`${p}-value-hint`}
          placeholder={item ? `•••• ${item.last4}` : undefined}
        />
      </Field>
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onCancel}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={save.isPending}>
          {t("common.save")}
        </Btn>
      </div>
    </form>
  );
}

function VaultRow({ item }: { item: SchemaVaultItemView }) {
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const del = useDeleteVaultItem();
  return (
    <li className="border-b border-rule py-3">
      <div className="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 sm:grid-cols-[1fr_7rem_8rem_auto]">
        <div className="min-w-0">
          <p className="truncate text-sm font-[var(--weight-medium)] text-ink">{item.name}</p>
          <p className="truncate text-xs text-ink-faint">
            {[item.site, item.username].filter(Boolean).join(" · ") || t("vault.noSite")}
          </p>
        </div>
        <span className="label-caps hidden sm:inline">{t(`vault.kinds.${item.kind}`)}</span>
        <span
          className="figures order-3 col-span-2 text-ink-soft sm:order-none sm:col-span-1"
          aria-label={t("vault.endsWith", { last4: item.last4 })}
        >
          •••• {item.last4}
        </span>
        <div className="flex items-center gap-1 justify-self-end">
          <Btn kind="quiet" size="sm" aria-expanded={editing} onClick={() => setEditing((e) => !e)}>
            {editing ? t("common.close") : t("common.edit")}
          </Btn>
          {confirm ? (
            <>
              <Btn
                kind="danger"
                size="sm"
                busy={del.isPending}
                onClick={() =>
                  del.mutate(item.id, {
                    onSuccess: () => toast.success(t("vault.deleted", { name: item.name })),
                    onError: (err) => toast.error(t("vault.deleteFailed"), { description: errorText(err) }),
                  })
                }
              >
                {t("common.delete")}
              </Btn>
              <Btn kind="quiet" size="sm" onClick={() => setConfirm(false)}>
                {t("common.cancel")}
              </Btn>
            </>
          ) : (
            <Btn
              kind="quiet"
              size="sm"
              aria-label={t("vault.delete", { name: item.name })}
              onClick={() => setConfirm(true)}
            >
              <Trash2 aria-hidden className="size-3.5" />
            </Btn>
          )}
        </div>
      </div>
      <p className="figures mt-0.5 text-ink-faint">{t("vault.updated", { when: formatDay(item.updated_at) })}</p>
      {editing && (
        <div className="mt-3 rounded-sm border border-rule bg-surface p-4">
          <VaultForm
            item={item}
            onDone={() => {
              setEditing(false);
              toast.success(t("vault.saved"));
            }}
            onCancel={() => setEditing(false)}
          />
        </div>
      )}
    </li>
  );
}

export function VaultPage() {
  const vault = useVault();
  const [adding, setAdding] = useState(false);
  return (
    <div className="max-w-3xl">
      <Section
        id="vault"
        title={t("vault.title")}
        aside={
          !adding && (
            <Btn size="sm" onClick={() => setAdding(true)}>
              <Plus aria-hidden className="size-3.5" />
              {t("vault.add")}
            </Btn>
          )
        }
      >
        <p className="mb-3 text-sm text-ink-soft">{t("vault.body")}</p>
        {adding && (
          <div className="mb-4 rounded-sm border border-rule-strong bg-surface-raised p-4 shadow-[var(--shadow-1)]">
            <h3 className="mb-3 text-sm font-[var(--weight-strong)] text-ink">{t("vault.add")}</h3>
            <VaultForm
              onDone={(v) => {
                setAdding(false);
                toast.success(t("vault.created", { name: v.name }));
              }}
              onCancel={() => setAdding(false)}
            />
          </div>
        )}
        {vault.isPending && (
          <p className="text-sm text-ink-faint" role="status">
            {t("common.loading")}
          </p>
        )}
        {vault.isError && (
          <Notice icon={CircleAlert} tone="danger" title={t("vault.loadFailed")}>
            {errorText(vault.error)}
          </Notice>
        )}
        {vault.isSuccess && vault.data.length === 0 && !adding && (
          <Notice icon={KeyRound} title={t("vault.empty.title")}>
            {t("vault.empty.body")}
          </Notice>
        )}
        {vault.isSuccess && vault.data.length > 0 && (
          <ul className="border-t border-rule">
            {vault.data.map((i) => (
              <VaultRow key={i.id} item={i} />
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}
