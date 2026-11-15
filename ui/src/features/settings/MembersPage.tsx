import { useState } from "react";
import { Check, CircleAlert, Copy, Link2, Server } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useCreateInvite,
  useInstance,
  useInvites,
  useMembers,
  useRemoveMember,
  useRevokeInvite,
  useSetMemberRole,
} from "@/api/hooks";
import type { SchemaMemberView } from "@/api/schema";
import { Notice, Section } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";
import { NativeSelect } from "./ProviderForm";

type Role = "owner" | "member";
const ROLES: Role[] = ["owner", "member"];

function MemberRow({ m }: { m: SchemaMemberView }) {
  const setRole = useSetMemberRole();
  const remove = useRemoveMember();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const name = m.name || m.email;
  const id = `member-role-${m.user_id}`;
  return (
    <li className="border-b border-rule py-2.5">
      <div className="grid grid-cols-1 gap-x-4 gap-y-2 sm:grid-cols-[1fr_auto] sm:items-center">
        <div className="min-w-0">
          <p className="flex items-center gap-1.5 text-sm font-[var(--weight-medium)] text-ink">
            <span className="truncate">{name}</span>
            {m.you && (
              <span className="shrink-0 rounded-xs border border-rule px-1 text-2xs font-[var(--weight-regular)] text-ink-soft">
                {t("members.you")}
              </span>
            )}
          </p>
          <p className="truncate text-xs text-ink-faint">
            {m.name && `${m.email} · `}
            {t("members.since", { when: formatDay(m.since) })}
          </p>
        </div>
        <div className="flex items-center gap-1.5">
          <label htmlFor={id} className="sr-only">
            {t("members.role", { name })}
          </label>
          <NativeSelect
            id={id}
            value={m.role}
            onChange={(v) => {
              setError(null);
              setRole.mutate(
                { user: m.user_id, role: v as Role },
                {
                  onSuccess: () => toast.success(t("members.roleSaved")),
                  onError: (err) => setError(errorText(err)),
                },
              );
            }}
            className="w-32 [&_select]:h-8 [&_select]:text-sm"
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {t(`members.roles.${r}`)}
              </option>
            ))}
          </NativeSelect>
          {!confirm && (
            <Btn
              kind="danger"
              size="sm"
              aria-label={t("members.removeName", { name })}
              onClick={() => setConfirm(true)}
            >
              {t("members.remove")}
            </Btn>
          )}
        </div>
      </div>
      {confirm && (
        <div className="mt-2 flex flex-wrap items-center gap-2 rounded-sm border border-rule bg-surface-sunk px-3 py-2">
          <p className="mr-auto text-sm text-ink">{t("members.confirm", { name })}</p>
          <Btn kind="quiet" size="sm" onClick={() => setConfirm(false)}>
            {t("common.cancel")}
          </Btn>
          <Btn
            kind="danger"
            size="sm"
            busy={remove.isPending}
            autoFocus
            onClick={() => {
              setError(null);
              remove.mutate(m.user_id, {
                onSuccess: () => toast.success(t("members.removed", { name })),
                onError: (err) => {
                  setConfirm(false);
                  setError(errorText(err));
                },
              });
            }}
          >
            {t("members.removeName", { name })}
          </Btn>
        </div>
      )}
      {error && (
        <div className="mt-2">
          <Notice icon={CircleAlert} tone="danger" title={t("members.failed")}>
            {error}
          </Notice>
        </div>
      )}
    </li>
  );
}

function Members() {
  const members = useMembers();
  if (members.isPending)
    return (
      <p className="text-sm text-ink-faint" role="status">
        {t("common.loading")}
      </p>
    );
  if (members.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("members.loadFailed")}>
        {errorText(members.error)}
      </Notice>
    );
  return (
    <ul className="border-t border-rule" aria-label={t("members.list")}>
      {members.data.map((m) => (
        <MemberRow key={m.user_id} m={m} />
      ))}
    </ul>
  );
}

function CopyLink({ url }: { url: string }) {
  const [done, setDone] = useState(false);
  return (
    <Btn
      size="sm"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(url);
          setDone(true);
          toast.success(t("invites.copied"));
          setTimeout(() => setDone(false), 2000);
        } catch {
          toast.error(t("invites.copyFailed"));
        }
      }}
    >
      {done ? <Check aria-hidden className="size-3.5" /> : <Copy aria-hidden className="size-3.5" />}
      {done ? t("invites.copied") : t("invites.copy")}
    </Btn>
  );
}

function Invites() {
  const invites = useInvites(true);
  const create = useCreateInvite();
  const revoke = useRevokeInvite();
  const [role, setRole] = useState<Role>("member");
  const [days, setDays] = useState("7");
  const [daysError, setDaysError] = useState<string | null>(null);
  const [fresh, setFresh] = useState<string | null>(null);
  return (
    <>
      <p className="mb-3 max-w-[var(--measure)] text-sm text-ink-soft">{t("invites.intro")}</p>
      <form
        className="mb-4 flex flex-wrap items-start gap-3"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          const n = Number(days);
          if (!Number.isInteger(n) || n < 1 || n > 30) return setDaysError(t("invites.needDays"));
          create.mutate(
            { role, days: n },
            {
              onSuccess: (v) => setFresh(v.url ?? null),
              onError: (err) => toast.error(t("invites.failed"), { description: errorText(err) }),
            },
          );
        }}
      >
        <Field id="invite-role" label={t("invites.role")} className="w-36">
          <NativeSelect id="invite-role" value={role} onChange={(v) => setRole(v as Role)}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {t(`members.roles.${r}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field
          id="invite-days"
          label={t("invites.days")}
          hint={t("invites.daysHint")}
          error={daysError}
          className="w-44"
        >
          <input
            id="invite-days"
            value={days}
            inputMode="numeric"
            aria-invalid={!!daysError || undefined}
            aria-describedby={daysError ? "invite-days-error" : "invite-days-hint"}
            onChange={(e) => {
              setDays(e.target.value);
              setDaysError(null);
            }}
            className={cn(inputCls, "font-mono text-sm")}
          />
        </Field>
        <Btn type="submit" busy={create.isPending} className="sm:mt-6">
          <Link2 aria-hidden className="size-3.5" />
          {t("invites.create")}
        </Btn>
      </form>
      {fresh && (
        <div className="mb-4 rounded-sm border border-glaze/50 bg-glaze-wash px-4 py-3" role="status">
          <p className="text-sm text-ink">{t("invites.fresh")}</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <p className="min-w-0 flex-1 font-mono text-sm break-all text-ink select-all">{fresh}</p>
            <CopyLink url={fresh} />
          </div>
        </div>
      )}
      {invites.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("invites.loadFailed")}>
          {errorText(invites.error)}
        </Notice>
      )}
      {invites.isPending && (
        <p className="text-sm text-ink-faint" role="status">
          {t("common.loading")}
        </p>
      )}
      {invites.isSuccess && (
        <ul aria-label={t("invites.list")}>
          {invites.data.length === 0 && (
            <li className="border-y border-rule py-3 text-sm text-ink-faint">{t("invites.none")}</li>
          )}
          {invites.data.map((inv, i) => (
            <li
              key={inv.id}
              className={cn(
                "grid grid-cols-[1fr_auto] items-center gap-3 border-b border-rule py-2.5",
                i === 0 && "border-t",
              )}
            >
              <div className="min-w-0">
                <p className="text-sm font-[var(--weight-medium)] text-ink">{t(`members.roles.${inv.role}`)}</p>
                <p className="figures text-ink-faint">{t("invites.expires", { when: formatDay(inv.expires_at) })}</p>
              </div>
              <Btn
                kind="danger"
                size="sm"
                aria-label={t("invites.revokeName", {
                  role: t(`members.roles.${inv.role}`).toLowerCase(),
                  when: formatDay(inv.expires_at),
                })}
                onClick={() =>
                  revoke.mutate(inv.id, {
                    onSuccess: () => toast.success(t("invites.revoked")),
                    onError: (err) => toast.error(t("invites.failed"), { description: errorText(err) }),
                  })
                }
              >
                {t("invites.revoke")}
              </Btn>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

export function MembersPage() {
  const instance = useInstance();
  return (
    <div className="max-w-3xl">
      <Section id="members" title={t("members.title")}>
        <Members />
      </Section>
      <Section id="invites" title={t("invites.title")}>
        {instance.isPending && (
          <p className="text-sm text-ink-faint" role="status">
            {t("common.loading")}
          </p>
        )}
        {instance.isError && (
          <Notice icon={CircleAlert} tone="danger" title={t("admin.instance.loadFailed")}>
            {errorText(instance.error)}
          </Notice>
        )}
        {instance.data?.mode === "local" && <Notice icon={Server} title={t("invites.localOnly")} />}
        {instance.data?.mode === "server" && <Invites />}
      </Section>
    </div>
  );
}
