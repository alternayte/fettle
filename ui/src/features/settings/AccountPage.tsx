import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, ChevronRight, Fingerprint, KeyRound } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { PasskeyError, addPasskey, deletePasskey, listPasskeys, passkeysSupported } from "@/api/signin";
import { useSession, useUpdateMe } from "@/api/hooks";
import { Section } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { has, t } from "@/lib/copy";
import { formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";

function NameForm() {
  const { me } = useSession();
  const update = useUpdateMe();
  const [name, setName] = useState(me.name);
  const [error, setError] = useState<string | null>(null);
  const dirty = name.trim() !== me.name;
  return (
    <form
      className="space-y-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim()) return setError(t("account.needName"));
        update.mutate(name.trim(), {
          onSuccess: () => toast.success(t("account.saved")),
          onError: (err) => toast.error(t("account.failed"), { description: errorText(err) }),
        });
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="account-name" label={t("account.name")} hint={t("account.nameHint")} error={error}>
          <input
            id="account-name"
            value={name}
            maxLength={80}
            autoComplete="name"
            aria-invalid={!!error || undefined}
            aria-describedby={error ? "account-name-error" : "account-name-hint"}
            onChange={(e) => {
              setName(e.target.value);
              setError(null);
            }}
            className={inputCls}
          />
        </Field>
        <Field id="account-email" label={t("account.email")} hint={t("account.emailHint")}>
          <input
            id="account-email"
            value={me.email}
            readOnly
            aria-describedby="account-email-hint"
            className={cn(inputCls, "bg-surface-sunk text-ink-soft")}
          />
        </Field>
      </div>
      <div className="flex justify-end">
        <Btn type="submit" kind="primary" size="sm" disabled={!dirty} busy={update.isPending}>
          {t("common.save")}
        </Btn>
      </div>
    </form>
  );
}

function passkeyError(err: unknown) {
  if (err instanceof PasskeyError && err.code === "CANCELLED") return;
  if (err instanceof DOMException && err.name === "NotAllowedError") return;
  const stale = err instanceof PasskeyError && err.code === "SESSION_NOT_FRESH";
  toast.error(t("account.passkeys.failed"), { description: stale ? t("account.passkeys.stale") : errorText(err) });
}

function Passkeys() {
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["passkeys"], queryFn: listPasskeys });
  const [name, setName] = useState(t("account.passkeys.defaultName"));
  const [busy, setBusy] = useState(false);
  const supported = passkeysSupported();
  return (
    <Section id="passkeys" title={t("account.passkeys.title")}>
      <p className="mb-4 max-w-[var(--measure)] text-sm text-ink-soft">{t("account.passkeys.intro")}</p>
      {supported ? (
        <form
          className="mb-4 flex flex-wrap items-end gap-2"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              await addPasskey(name.trim() || t("account.passkeys.defaultName"));
              toast.success(t("account.passkeys.added"));
              void qc.invalidateQueries({ queryKey: ["passkeys"] });
            } catch (err) {
              passkeyError(err);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field id="passkey-name" label={t("account.passkeys.name")} className="w-64">
            <input
              id="passkey-name"
              value={name}
              maxLength={80}
              onChange={(e) => setName(e.target.value)}
              className={inputCls}
            />
          </Field>
          <Btn type="submit" busy={busy}>
            <Fingerprint aria-hidden className="size-4" />
            {t("account.passkeys.add")}
          </Btn>
        </form>
      ) : (
        <p className="mb-4 text-sm text-ink-faint">{t("account.passkeys.unsupported")}</p>
      )}
      <ul aria-label={t("account.passkeys.title")}>
        {(keys.data ?? []).length === 0 && (
          <li className="border-y border-rule py-3 text-sm text-ink-faint">{t("account.passkeys.none")}</li>
        )}
        {(keys.data ?? []).map((k) => (
          <li key={k.id} className="grid grid-cols-[1fr_auto] items-center gap-3 border-b border-rule py-2.5">
            <div className="min-w-0">
              <p className="truncate text-sm font-[var(--weight-medium)] text-ink">{k.name}</p>
              <p className="figures text-ink-faint">
                {k.lastUsedAt
                  ? t("account.passkeys.lastUsed", { when: formatDay(k.lastUsedAt) })
                  : t("account.passkeys.neverUsed")}
              </p>
            </div>
            <Btn
              kind="danger"
              size="sm"
              onClick={async () => {
                try {
                  await deletePasskey(k.id);
                  toast.success(t("account.passkeys.removed"));
                  void qc.invalidateQueries({ queryKey: ["passkeys"] });
                } catch (err) {
                  passkeyError(err);
                }
              }}
            >
              {t("account.passkeys.remove")}
            </Btn>
          </li>
        ))}
      </ul>
    </Section>
  );
}

const linkCls =
  "grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b border-rule px-1 py-2.5 hover:bg-surface-sunk sm:px-2";

export function AccountPage() {
  const { me, space } = useSession();
  const role = has(`members.roles.${space.role}`) ? t(`members.roles.${space.role}`) : space.role;
  return (
    <div className="max-w-3xl">
      <Section id="account" title={t("account.title")}>
        <p className="mb-4 text-sm text-ink-soft">
          {t("account.role", { space: space.name, role: role.toLowerCase() })}
        </p>
        <NameForm />
      </Section>
      {me.mode === "server" && <Passkeys />}
      <Section id="account-more" title={t("account.more")}>
        <ul className="border-t border-rule">
          <li>
            <Link to="/settings/tokens" className={linkCls}>
              <KeyRound aria-hidden className="size-4 text-ink-faint" />
              <span className="min-w-0">
                <span className="block text-sm font-[var(--weight-medium)] text-ink">{t("settings.tabs.tokens")}</span>
                <span className="block text-xs text-ink-faint">{t("account.tokensBody")}</span>
              </span>
              <ChevronRight aria-hidden className="size-4 text-ink-faint" />
            </Link>
          </li>
          <li>
            <Link to="/settings/notifications" className={linkCls}>
              <Bell aria-hidden className="size-4 text-ink-faint" />
              <span className="min-w-0">
                <span className="block text-sm font-[var(--weight-medium)] text-ink">
                  {t("settings.tabs.notifications")}
                </span>
                <span className="block text-xs text-ink-faint">{t("account.notifyBody")}</span>
              </span>
              <ChevronRight aria-hidden className="size-4 text-ink-faint" />
            </Link>
          </li>
        </ul>
      </Section>
    </div>
  );
}
