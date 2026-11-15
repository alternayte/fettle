import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { CircleAlert, Link2Off } from "lucide-react";
import { api, ApiError, errorText, must } from "@/api/client";
import { useMe } from "@/api/hooks";
import { signIn } from "@/api/signin";
import { Notice } from "@/app/Page";
import { FullPage } from "@/app/Root";
import { Btn, Field, inputCls } from "@/components/form";
import { has, t } from "@/lib/copy";
import { formatDay } from "@/lib/format";

async function signUp(token: string, body: { name: string; email: string; password: string }) {
  const r = await fetch(`/api/v1/invites/${encodeURIComponent(token)}/signup`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const b = (await r.json().catch(() => ({}))) as { detail?: string };
    throw new ApiError(r.status, b.detail ?? t("invite.failed"));
  }
}

function Problem({ text }: { text: string | null }) {
  if (!text) return null;
  return (
    <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
      {text}
    </p>
  );
}

function SignUpForm({ token, onDone }: { token: string; onDone: () => Promise<void> }) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      className="mt-4 space-y-4"
      noValidate
      onSubmit={async (e) => {
        e.preventDefault();
        setError(null);
        setBusy(true);
        try {
          await signUp(token, { name: name.trim(), email: email.trim(), password });
          await onDone();
        } catch (err) {
          setError(errorText(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <Field id="invite-name" label={t("invite.name")}>
        <input
          id="invite-name"
          autoComplete="name"
          maxLength={80}
          value={name}
          onChange={(e) => setName(e.target.value)}
          className={inputCls}
        />
      </Field>
      <Field id="invite-email" label={t("invite.email")}>
        <input
          id="invite-email"
          type="email"
          autoComplete="username"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className={inputCls}
        />
      </Field>
      <Field id="invite-password" label={t("invite.password")}>
        <input
          id="invite-password"
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className={inputCls}
        />
      </Field>
      <Problem text={error} />
      <Btn type="submit" kind="primary" busy={busy} className="w-full">
        {t("invite.create")}
      </Btn>
    </form>
  );
}

function SignInForm({ onDone }: { onDone: () => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      className="mt-4 space-y-4"
      noValidate
      onSubmit={async (e) => {
        e.preventDefault();
        setError(null);
        setBusy(true);
        try {
          const r = await signIn(email.trim(), password);
          if (r !== "ok") return setError(t(`auth.signIn.${r}`));
          await onDone();
        } catch (err) {
          setError(errorText(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <Field id="invite-signin-email" label={t("auth.signIn.email")}>
        <input
          id="invite-signin-email"
          type="email"
          autoComplete="username"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className={inputCls}
        />
      </Field>
      <Field id="invite-signin-password" label={t("auth.signIn.password")}>
        <input
          id="invite-signin-password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className={inputCls}
        />
      </Field>
      <Problem text={error} />
      <Btn type="submit" kind="primary" busy={busy} className="w-full">
        {t("invite.signInJoin")}
      </Btn>
    </form>
  );
}

/** InvitePage opens an invite link: a new person makes an account, a signed-in person joins. */
export function InvitePage() {
  const { token } = useParams({ strict: false }) as { token: string };
  const qc = useQueryClient();
  const navigate = useNavigate();
  const me = useMe();
  const [mode, setMode] = useState<"new" | "signin">("new");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const info = useQuery({
    queryKey: ["invite", token],
    retry: false,
    queryFn: () => must(api.GET("/api/v1/invites/{token}", { params: { path: { token } } })),
  });

  const accept = () => must(api.POST("/api/v1/invites/{token}/accept", { params: { path: { token } } }));
  const enter = async () => {
    await qc.invalidateQueries();
    await navigate({ to: "/", replace: true });
  };

  if (info.isPending || me.isPending) return <div className="h-full bg-ground" aria-busy="true" />;
  if (info.isError) {
    const gone = info.error instanceof ApiError && (info.error.status === 410 || info.error.status === 404);
    return (
      <FullPage>
        {gone ? (
          <Notice icon={Link2Off} title={t("invite.gone.title")}>
            {t("invite.gone.body")}
          </Notice>
        ) : (
          <Notice icon={CircleAlert} tone="danger" title={t("invite.loadFailed")}>
            {errorText(info.error)}
          </Notice>
        )}
      </FullPage>
    );
  }

  const inv = info.data;
  const role = has(`members.roles.${inv.role}`) ? t(`members.roles.${inv.role}`).toLowerCase() : inv.role;
  return (
    <FullPage>
      <h1 className="serif-title text-2xl text-ink">{t("invite.title", { space: inv.space })}</h1>
      <p className="mt-2 text-sm text-ink-soft">{t("invite.role", { role, when: formatDay(inv.expires_at) })}</p>
      {me.isSuccess ? (
        <div className="mt-4 space-y-4">
          <p className="text-sm text-ink">{t("invite.signedIn", { email: me.data.email })}</p>
          <Problem text={error} />
          <Btn
            kind="primary"
            className="w-full"
            busy={busy}
            onClick={async () => {
              setError(null);
              setBusy(true);
              try {
                await accept();
                await enter();
              } catch (err) {
                setError(errorText(err));
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("invite.join", { space: inv.space })}
          </Btn>
        </div>
      ) : (
        <>
          {mode === "new" ? (
            <SignUpForm token={token} onDone={enter} />
          ) : (
            <SignInForm
              onDone={async () => {
                await accept();
                await enter();
              }}
            />
          )}
          <p className="mt-4 text-sm text-ink-soft">
            {mode === "new" && <>{t("invite.haveAccount")} </>}
            <button
              type="button"
              onClick={() => setMode(mode === "new" ? "signin" : "new")}
              className="text-ink underline underline-offset-2 hover:text-ink-soft"
            >
              {mode === "new" ? t("invite.signIn") : t("invite.newAccount")}
            </button>
          </p>
        </>
      )}
    </FullPage>
  );
}
