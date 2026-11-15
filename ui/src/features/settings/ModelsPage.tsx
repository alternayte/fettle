import { useState } from "react";
import { ArrowUp, CircleAlert, Plus, Server, Trash2, Undo2, X } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useDeleteProvider,
  usePresets,
  useProviders,
  useRoles,
  useSetRole,
  useTestProvider,
  type ModelRole,
} from "@/api/hooks";
import type { SchemaProviderView, SchemaTarget } from "@/api/schema";
import { targetFor } from "./models";
import { Notice, Section } from "@/app/Page";
import { Btn } from "@/components/form";
import { t, tn } from "@/lib/copy";
import { ModelPicker } from "./ModelPicker";
import { NativeSelect, ProviderForm } from "./ProviderForm";
import { ProviderTest } from "./ProviderTest";

const ROLES: ModelRole[] = ["owner", "router", "verifier", "computer_use", "summarizer"];

function ProviderItem({ p }: { p: SchemaProviderView }) {
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const del = useDeleteProvider();
  const presets = usePresets();
  const presetName = presets.data?.find((x) => x.id === p.preset)?.name ?? p.preset;
  return (
    <li className="border-b border-rule py-4">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-1">
        <Server aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-faint" />
        <div className="min-w-0 flex-1">
          <h3 className="text-base font-[var(--weight-strong)] text-ink">{p.name}</h3>
          <p className="text-xs break-all text-ink-faint">
            {presetName} · <span className="font-mono">{p.base_url}</span>
          </p>
          <p className="figures mt-0.5 text-ink-soft">
            {p.has_key ? t("models.keyLast4", { last4: p.key_last4 }) : t("models.noKey")}
            {" · "}
            {t(`models.apiType.${p.api_type}`)}
            {" · "}
            {tn("models.modelCount", p.models?.length ?? 0)}
          </p>
        </div>
        <div className="flex items-center gap-1">
          <Btn kind="quiet" size="sm" aria-expanded={editing} onClick={() => setEditing((e) => !e)}>
            {editing ? t("common.close") : t("common.edit")}
          </Btn>
          {confirm ? (
            <span className="inline-flex items-center gap-1">
              <span className="text-xs text-ink-soft">{t("models.deleteConfirm")}</span>
              <Btn
                kind="danger"
                size="sm"
                busy={del.isPending}
                onClick={() =>
                  del.mutate(p.id, {
                    onError: (err) => toast.error(t("models.deleteFailed"), { description: errorText(err) }),
                  })
                }
              >
                {t("common.delete")}
              </Btn>
              <Btn kind="quiet" size="sm" onClick={() => setConfirm(false)}>
                {t("common.cancel")}
              </Btn>
            </span>
          ) : (
            <Btn
              kind="quiet"
              size="sm"
              aria-label={t("models.delete", { name: p.name })}
              onClick={() => setConfirm(true)}
            >
              <Trash2 aria-hidden className="size-3.5" />
            </Btn>
          )}
        </div>
      </div>
      {editing && (
        <div className="mt-4 rounded-sm border border-rule bg-surface p-4">
          <ProviderForm
            provider={p}
            idPrefix={`edit-${p.id}`}
            onSaved={() => {
              setEditing(false);
              toast.success(t("models.saved"));
            }}
            onCancel={() => setEditing(false)}
          />
        </div>
      )}
      <div className="mt-3 sm:pl-7">
        <ProviderTest provider={p} idPrefix={`test-${p.id}`} />
      </div>
    </li>
  );
}

function RoleRow({
  role,
  targets,
  providers,
  bot,
  inherited = [],
}: {
  role: ModelRole;
  targets: SchemaTarget[];
  providers: SchemaProviderView[];
  /** bot makes the row edit the override of one bot; inherited are the space targets of the role. */
  bot?: string;
  inherited?: SchemaTarget[];
}) {
  const setRole = useSetRole();
  const [providerId, setProviderId] = useState(providers[0]?.id ?? "");
  const [model, setModel] = useState("");
  const provider = providers.find((p) => p.id === providerId) ?? providers[0];
  const byId = Object.fromEntries(providers.map((p) => [p.id, p]));
  const save = (next: SchemaTarget[], ok: string) =>
    setRole.mutate(
      { role, targets: next, bot },
      {
        onSuccess: () => {
          setModel("");
          toast.success(ok);
        },
        onError: (err) => toast.error(t("models.roles.saveFailed"), { description: errorText(err) }),
      },
    );
  const pid = bot ? `bot-role-${role}` : `role-${role}`;
  return (
    <li className="grid gap-3 border-b border-rule py-4 md:grid-cols-[12rem_1fr]">
      <div>
        <h3 className="text-sm font-[var(--weight-strong)] text-ink">{t(`models.roles.${role}.name`)}</h3>
        <p className="text-xs text-ink-faint">{t(`models.roles.${role}.body`)}</p>
      </div>
      <div className="min-w-0 space-y-2">
        {targets.length === 0 ? (
          <p className="text-sm text-ink-faint">
            {bot && inherited.length > 0
              ? t("models.roles.botInherits", { models: inherited.map((x) => x.model).join(", ") })
              : role === "owner"
                ? t("models.roles.ownerMissing")
                : bot
                  ? t("models.roles.botInheritsOwner")
                  : t("models.roles.inherit")}
          </p>
        ) : (
          <ol className="space-y-1" aria-label={t("models.roles.order", { role: t(`models.roles.${role}.name`) })}>
            {targets.map((tg, i) => (
              <li
                key={`${tg.provider_id}-${tg.model}-${i}`}
                className="flex items-center gap-2 rounded-xs border border-rule bg-surface px-2 py-1"
              >
                <span className="figures w-4 text-right text-ink-faint">{i + 1}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink">{tg.model}</span>
                <span className="hidden truncate text-xs text-ink-faint sm:inline">
                  {byId[tg.provider_id]?.name ?? t("models.roles.missingProvider")}
                </span>
                {i > 0 && (
                  <button
                    type="button"
                    aria-label={t("models.roles.moveUp", { model: tg.model })}
                    onClick={() => {
                      const next = [...targets];
                      [next[i - 1], next[i]] = [next[i], next[i - 1]];
                      save(next, t("models.roles.saved"));
                    }}
                    className="inline-flex size-6 items-center justify-center rounded-xs text-ink-faint hover:bg-surface-sunk hover:text-ink"
                  >
                    <ArrowUp aria-hidden className="size-3.5" />
                  </button>
                )}
                <button
                  type="button"
                  aria-label={t("models.roles.remove", { model: tg.model })}
                  onClick={() =>
                    save(
                      targets.filter((_, j) => j !== i),
                      t("models.roles.saved"),
                    )
                  }
                  className="inline-flex size-6 items-center justify-center rounded-xs text-ink-faint hover:bg-surface-sunk hover:text-danger"
                >
                  <X aria-hidden className="size-3.5" />
                </button>
              </li>
            ))}
          </ol>
        )}
        {provider && (
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <label htmlFor={`${pid}-provider`} className="sr-only">
              {t("models.roles.provider")}
            </label>
            {providers.length > 1 && (
              <NativeSelect
                id={`${pid}-provider`}
                value={provider.id}
                onChange={(v) => {
                  setProviderId(v);
                  setModel("");
                }}
                className="sm:w-44"
              >
                {providers.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </NativeSelect>
            )}
            <div className="min-w-0 flex-1">
              <label htmlFor={`${pid}-model`} className="sr-only">
                {t("models.roles.model")}
              </label>
              <ModelPicker id={`${pid}-model`} models={provider.models ?? []} value={model} onChange={setModel} />
            </div>
            <Btn
              size="md"
              disabled={!model}
              busy={setRole.isPending}
              onClick={() => save([...targets, targetFor(provider, model)], t("models.roles.saved"))}
            >
              <Plus aria-hidden className="size-3.5" />
              {targets.length > 0
                ? t("models.roles.addFallback")
                : bot
                  ? t("models.roles.override")
                  : t("models.roles.set")}
            </Btn>
          </div>
        )}
        {bot && targets.length > 0 && (
          <Btn kind="quiet" size="sm" busy={setRole.isPending} onClick={() => save([], t("models.roles.usingSpace"))}>
            <Undo2 aria-hidden className="size-3.5" />
            {t("models.roles.useSpace")}
          </Btn>
        )}
      </div>
    </li>
  );
}

/** ModelRoles lists the model roles of the space, or the overrides of one bot. */
export function ModelRoles({ bot }: { bot?: string }) {
  const providers = useProviders();
  const space = useRoles();
  const own = useRoles(bot);
  const roles = bot ? own : space;
  const list = providers.data ?? [];
  const targetsOf = (rs: typeof space.data, r: ModelRole) => rs?.find((x) => x.role === r)?.targets ?? [];
  return (
    <>
      {roles.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("models.roles.loadFailed")}>
          {errorText(roles.error)}
        </Notice>
      )}
      {list.length === 0 ? (
        <p className="text-sm text-ink-faint">{t("models.roles.needProvider")}</p>
      ) : (
        <ul className="border-t border-rule">
          {ROLES.map((r) => (
            <RoleRow
              key={r}
              role={r}
              providers={list}
              bot={bot}
              targets={targetsOf(roles.data, r)}
              inherited={bot ? targetsOf(space.data, r) : undefined}
            />
          ))}
        </ul>
      )}
    </>
  );
}

export function ModelsPage() {
  const providers = useProviders();
  const test = useTestProvider();
  const [adding, setAdding] = useState(false);
  const list = providers.data ?? [];
  return (
    <div className="max-w-3xl">
      <Section
        id="providers"
        title={t("models.providers")}
        aside={
          !adding && (
            <Btn size="sm" onClick={() => setAdding(true)}>
              <Plus aria-hidden className="size-3.5" />
              {t("models.addProvider")}
            </Btn>
          )
        }
      >
        {adding && (
          <div className="mb-4 rounded-sm border border-rule-strong bg-surface-raised p-4 shadow-[var(--shadow-1)]">
            <h3 className="mb-3 text-sm font-[var(--weight-strong)] text-ink">{t("models.addProvider")}</h3>
            <ProviderForm
              idPrefix="new"
              onSaved={(p) => {
                setAdding(false);
                toast.success(t("models.added", { name: p.name }));
                test.mutate({ id: p.id });
              }}
              onCancel={() => setAdding(false)}
            />
          </div>
        )}
        {providers.isPending && (
          <p className="text-sm text-ink-faint" role="status">
            {t("common.loading")}
          </p>
        )}
        {providers.isError && (
          <Notice icon={CircleAlert} tone="danger" title={t("models.loadFailed")}>
            {errorText(providers.error)}
          </Notice>
        )}
        {providers.isSuccess && list.length === 0 && !adding && (
          <Notice icon={Server} title={t("models.empty.title")}>
            {t("models.empty.body")}
          </Notice>
        )}
        {list.length > 0 && (
          <ul className="border-t border-rule">
            {list.map((p) => (
              <ProviderItem key={p.id} p={p} />
            ))}
          </ul>
        )}
      </Section>
      <Section id="roles" title={t("models.roles.title")}>
        <p className="mb-2 text-sm text-ink-soft">{t("models.roles.body")}</p>
        <ModelRoles />
      </Section>
    </div>
  );
}
