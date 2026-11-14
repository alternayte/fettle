import { useState } from "react";
import { LogOut } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useSession } from "@/api/hooks";
import { createToken, listTokens, revokeToken, signOut } from "@/api/signin";
import { Notice, Section } from "@/app/Page";
import { Btn, Field, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { formatDay } from "@/lib/format";

export function TokensPage() {
  const { me } = useSession();
  const qc = useQueryClient();
  const server = me.mode === "server";
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: listTokens, enabled: server });
  const [name, setName] = useState("");
  const [fresh, setFresh] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (!server) return <Notice title={t("tokens.localOnly")} />;
  return (
    <div>
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("tokens.intro")}</p>
      <Section id="tokens" title={t("settings.tabs.tokens")}>
        <form
          className="mb-4 flex flex-wrap items-end gap-2"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!name.trim()) return void toast.error(t("tokens.needName"));
            setBusy(true);
            try {
              const r = await createToken(name.trim());
              setFresh(r.plaintext);
              setName("");
              void qc.invalidateQueries({ queryKey: ["tokens"] });
            } catch (err) {
              toast.error(t("tokens.failed"), { description: errorText(err) });
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field id="token-name" label={t("tokens.name")} className="w-64">
            <input
              id="token-name"
              value={name}
              maxLength={80}
              onChange={(e) => setName(e.target.value)}
              className={inputCls}
            />
          </Field>
          <Btn type="submit" busy={busy}>
            {t("tokens.create")}
          </Btn>
        </form>
        {fresh && (
          <div className="mb-4 rounded-sm border border-glaze/50 bg-glaze-wash px-4 py-3">
            <p className="text-sm text-ink">{t("tokens.copy")}</p>
            <p className="mt-2 font-mono text-sm break-all text-ink select-all">{fresh}</p>
          </div>
        )}
        <ul aria-label={t("settings.tabs.tokens")}>
          {(tokens.data ?? []).length === 0 && (
            <li className="border-y border-rule py-3 text-sm text-ink-faint">{t("tokens.none")}</li>
          )}
          {(tokens.data ?? []).map((k) => (
            <li key={k.id} className="grid grid-cols-[1fr_auto] items-center gap-3 border-b border-rule py-2.5">
              <div className="min-w-0">
                <p className="truncate text-sm font-[var(--weight-medium)] text-ink">{k.name}</p>
                <p className="figures text-ink-faint">
                  {k.start}… ·{" "}
                  {k.lastUsedAt ? t("tokens.lastUsed", { when: formatDay(k.lastUsedAt) }) : t("tokens.neverUsed")}
                </p>
              </div>
              {k.revokedAt ? (
                <span className="text-sm text-ink-faint">{t("tokens.revoked")}</span>
              ) : (
                <Btn
                  kind="danger"
                  size="sm"
                  onClick={async () => {
                    try {
                      await revokeToken(k.id);
                      void qc.invalidateQueries({ queryKey: ["tokens"] });
                    } catch (err) {
                      toast.error(t("tokens.failed"), { description: errorText(err) });
                    }
                  }}
                >
                  {t("tokens.revoke")}
                </Btn>
              )}
            </li>
          ))}
        </ul>
      </Section>
      <Btn
        className="mt-8"
        kind="quiet"
        onClick={async () => {
          await signOut();
          await qc.invalidateQueries();
        }}
      >
        <LogOut aria-hidden className="size-3.5" />
        {t("tokens.signOut")}
      </Btn>
    </div>
  );
}
