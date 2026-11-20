import { useState } from "react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useCreatePairing, useExtensionTokens, useRevokeExtensionToken, useSession } from "@/api/hooks";
import { Section } from "@/app/Page";
import { Btn } from "@/components/form";
import { t } from "@/lib/copy";
import { formatDay } from "@/lib/format";

type Pairing = { code: string; url: string; server: string };

/**
 * ChromeExtension pairs the Fettle extension for Chrome, which shares a sign-in with a bot
 * (docs/specs/share-a-sign-in.md). The pairing page opens in the default browser; when that
 * is not Chrome, the person types the server and the code into the extension.
 */
export function ChromeExtension() {
  const { me } = useSession();
  const local = me.mode !== "server";
  const pair = useCreatePairing();
  const tokens = useExtensionTokens(local);
  const revoke = useRevokeExtensionToken();
  const [pairing, setPairing] = useState<Pairing | null>(null);
  return (
    <Section id="chrome" title={t("chrome.title")}>
      <p className="mb-4 max-w-[var(--measure)] text-sm text-ink-soft">{t("chrome.intro")}</p>
      <Btn
        kind="primary"
        busy={pair.isPending}
        onClick={() =>
          pair.mutate(undefined, {
            onSuccess: (p) => {
              setPairing(p);
              window.open(p.url, "_blank", "noopener");
            },
            onError: (err) => toast.error(t("chrome.failed"), { description: errorText(err) }),
          })
        }
      >
        {t("chrome.connect")}
      </Btn>
      {pairing && (
        <div className="mt-4 max-w-[var(--measure)] rounded-sm border border-glaze/50 bg-glaze-wash px-4 py-3 text-sm">
          <p className="text-ink">{t("chrome.opened")}</p>
          <p className="mt-2 text-ink-soft">{t("chrome.typeCode")}</p>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            <dt className="text-ink-faint">{t("chrome.server")}</dt>
            <dd className="font-mono break-all text-ink select-all">{pairing.server}</dd>
            <dt className="text-ink-faint">{t("chrome.code")}</dt>
            <dd className="font-mono text-ink select-all">{pairing.code}</dd>
          </dl>
          <p className="mt-2 text-xs text-ink-faint">{t("chrome.expires")}</p>
        </div>
      )}
      {local ? (
        <ul aria-label={t("chrome.tokens")} className="mt-6">
          {(tokens.data ?? []).length === 0 && (
            <li className="border-y border-rule py-3 text-sm text-ink-faint">{t("chrome.none")}</li>
          )}
          {(tokens.data ?? []).map((k) => (
            <li key={k.id} className="grid grid-cols-[1fr_auto] items-center gap-3 border-b border-rule py-2.5">
              <div className="min-w-0">
                <p className="truncate text-sm font-[var(--weight-medium)] text-ink">
                  {t("chrome.token", { last4: k.last4 })}
                </p>
                <p className="figures text-ink-faint">
                  {t("chrome.paired", { when: formatDay(k.created_at) })} ·{" "}
                  {k.last_used_at ? t("tokens.lastUsed", { when: formatDay(k.last_used_at) }) : t("tokens.neverUsed")}
                </p>
              </div>
              <Btn
                kind="danger"
                size="sm"
                busy={revoke.isPending && revoke.variables === k.id}
                onClick={() =>
                  revoke.mutate(k.id, {
                    onError: (err) => toast.error(t("chrome.failed"), { description: errorText(err) }),
                  })
                }
              >
                {t("tokens.revoke")}
              </Btn>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-4 max-w-[var(--measure)] text-sm text-ink-faint">{t("chrome.serverTokens")}</p>
      )}
    </Section>
  );
}
