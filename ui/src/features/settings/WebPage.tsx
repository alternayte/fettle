import { useState } from "react";
import { CircleAlert, FlaskConical, Globe, ScrollText, SearchX } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useDeleteWebSearch, useSetWebSearch, useTestWebSearch, useWebSearch } from "@/api/hooks";
import type { SchemaSetWebSearchRequest, SchemaWebSearchView } from "@/api/schema";
import { Notice, Section } from "@/app/Page";
import { Btn, Field, SecretInput, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { formatUSD } from "@/lib/format";
import { cn } from "@/lib/utils";
import { NativeSelect } from "./ProviderForm";

type Kind = NonNullable<SchemaSetWebSearchRequest["kind"]>;
const KINDS: Kind[] = ["tavily", "exa", "searxng", "brave"];
const BRAVE_TERMS = "https://api-dashboard.search.brave.com/terms-of-service";

/** ProviderForm sets the one search provider of the space. A key stays when the kind stays. */
function SearchProviderForm({ current }: { current: SchemaWebSearchView | null }) {
  const set = useSetWebSearch();
  const [kind, setKind] = useState<Kind>(current?.kind ?? "tavily");
  const [baseURL, setBaseURL] = useState(current?.base_url ?? "");
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const keepKey = !!current?.has_key && current.kind === kind;
  const needsKey = kind !== "searxng";

  const submit = () => {
    setError(null);
    if (kind === "searxng" && !/^https?:\/\/\S+$/.test(baseURL.trim())) return setError(t("web.form.needURL"));
    if (needsKey && !keepKey && !key.trim())
      return setError(t("web.form.needKey", { provider: t(`web.kinds.${kind}`) }));
    set.mutate(
      { kind, base_url: kind === "searxng" ? baseURL.trim() : undefined, key: key.trim() || undefined },
      {
        onSuccess: () => {
          setKey("");
          toast.success(t("web.form.saved", { provider: t(`web.kinds.${kind}`) }));
        },
        onError: (err) => setError(errorText(err)),
      },
    );
  };

  return (
    <form
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="space-y-4"
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="web-kind" label={t("web.form.provider")} hint={t(`web.price.${kind}`)}>
          <NativeSelect id="web-kind" value={kind} onChange={(v) => setKind(v as Kind)} describedBy="web-kind-hint">
            {KINDS.map((k) => (
              <option key={k} value={k}>
                {t(`web.kinds.${k}`)}
              </option>
            ))}
          </NativeSelect>
        </Field>
        {kind === "searxng" && (
          <Field id="web-base" label={t("web.form.baseURL")} hint={t("web.form.baseURLHint")}>
            <input
              id="web-base"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              spellCheck={false}
              inputMode="url"
              placeholder="https://search.example.com"
              aria-describedby="web-base-hint"
              className={cn(inputCls, "font-mono text-sm")}
            />
          </Field>
        )}
      </div>
      <Field
        id="web-key"
        label={t(needsKey ? "web.form.key" : "web.form.keyOptional", { provider: t(`web.kinds.${kind}`) })}
        hint={keepKey ? t("web.form.keyKeep", { last4: current?.key_last4 ?? "" }) : t("web.form.keyHint")}
      >
        <SecretInput
          id="web-key"
          value={key}
          onChange={setKey}
          describedBy="web-key-hint"
          placeholder={keepKey ? `•••• ${current?.key_last4}` : undefined}
        />
      </Field>
      {kind === "brave" && (
        <Notice icon={ScrollText} title={t("web.brave.title")}>
          <p>{t("web.brave.body")}</p>
          <a href={BRAVE_TERMS} target="_blank" rel="noreferrer" className="text-ink underline underline-offset-2">
            {t("web.brave.link")}
          </a>
        </Notice>
      )}
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end">
        <Btn type="submit" kind="primary" busy={set.isPending}>
          {t("common.save")}
        </Btn>
      </div>
    </form>
  );
}

/** SearchTest runs one search with the stored provider and lists the results. */
function SearchTest() {
  const test = useTestWebSearch();
  const [query, setQuery] = useState("");
  const results = test.data?.results ?? [];
  return (
    <div className="space-y-3">
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (query.trim()) test.mutate(query.trim());
        }}
        className="flex flex-col gap-2 sm:flex-row sm:items-end"
      >
        <Field id="web-query" label={t("web.test.query")} className="flex-1">
          <input
            id="web-query"
            value={query}
            maxLength={400}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("web.test.placeholder")}
            className={inputCls}
          />
        </Field>
        <Btn type="submit" busy={test.isPending} disabled={!query.trim()}>
          <FlaskConical aria-hidden className="size-3.5" />
          {t("web.test.run")}
        </Btn>
      </form>
      <div aria-live="polite">
        {test.isError && (
          <Notice icon={CircleAlert} tone="danger" title={t("web.test.failed")}>
            {errorText(test.error)}
          </Notice>
        )}
        {test.isSuccess && (
          <>
            <p className="figures mb-2 text-ink-faint">
              {t("web.test.summary", { n: results.length, ms: test.data.ms, cost: formatUSD(test.data.cost_usd) })}
            </p>
            {results.length === 0 ? (
              <p className="text-sm text-ink-soft">{t("web.test.none")}</p>
            ) : (
              <ol className="space-y-3 border-t border-rule pt-3">
                {results.map((r, i) => (
                  <li key={`${r.url}-${i}`} className="min-w-0">
                    <a
                      href={r.url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-sm font-[var(--weight-medium)] text-ink underline underline-offset-2"
                    >
                      {r.title || r.url}
                    </a>
                    <p className="truncate font-mono text-xs text-ink-faint">{r.url}</p>
                    {r.snippet && <p className="mt-0.5 line-clamp-2 text-sm text-ink-soft">{r.snippet}</p>}
                  </li>
                ))}
              </ol>
            )}
          </>
        )}
      </div>
    </div>
  );
}

export function WebPage() {
  const provider = useWebSearch();
  const remove = useDeleteWebSearch();
  const current = provider.data ?? null;
  return (
    <div className="max-w-3xl">
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("web.intro")}</p>
      {provider.isPending && (
        <p className="text-sm text-ink-faint" role="status">
          {t("common.loading")}
        </p>
      )}
      {provider.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("web.loadFailed")}>
          {errorText(provider.error)}
        </Notice>
      )}
      {provider.isSuccess && (
        <>
          <Section
            id="web-search"
            title={t("web.search.title")}
            aside={
              current && (
                <Btn
                  kind="danger"
                  size="sm"
                  busy={remove.isPending}
                  onClick={() =>
                    remove.mutate(undefined, {
                      onSuccess: () => toast.success(t("web.search.removed")),
                      onError: (err) => toast.error(t("web.search.removeFailed"), { description: errorText(err) }),
                    })
                  }
                >
                  {t("web.search.remove")}
                </Btn>
              )
            }
          >
            <div className="mb-4">
              {current ? (
                <p className="text-sm text-ink">
                  {t("web.search.on", { provider: t(`web.kinds.${current.kind}`) })}
                  {current.has_key && (
                    <span className="figures ml-2 text-ink-faint">
                      {t("web.search.keyEnds", { last4: current.key_last4 })}
                    </span>
                  )}
                </p>
              ) : (
                <Notice icon={SearchX} title={t("web.search.offTitle")}>
                  {t("web.search.offBody")}
                </Notice>
              )}
            </div>
            <SearchProviderForm key={`${current?.kind ?? "none"}-${current?.key_last4 ?? ""}`} current={current} />
          </Section>
          {current && (
            <Section id="web-test" title={t("web.test.title")}>
              <SearchTest />
            </Section>
          )}
          <Section id="web-fetch" title={t("web.fetch.title")}>
            <Notice icon={Globe} title={t("web.fetch.noteTitle")}>
              {t("web.fetch.body")}
            </Notice>
          </Section>
        </>
      )}
    </div>
  );
}
