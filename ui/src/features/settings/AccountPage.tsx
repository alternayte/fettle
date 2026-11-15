import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { Bell, ChevronRight, KeyRound } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useSession, useUpdateMe } from "@/api/hooks";
import { Section } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { has, t } from "@/lib/copy";
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

const linkCls =
  "grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b border-rule px-1 py-2.5 hover:bg-surface-sunk sm:px-2";

export function AccountPage() {
  const { space } = useSession();
  const role = has(`members.roles.${space.role}`) ? t(`members.roles.${space.role}`) : space.role;
  return (
    <div className="max-w-3xl">
      <Section id="account" title={t("account.title")}>
        <p className="mb-4 text-sm text-ink-soft">
          {t("account.role", { space: space.name, role: role.toLowerCase() })}
        </p>
        <NameForm />
      </Section>
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
