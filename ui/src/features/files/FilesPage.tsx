import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { CircleAlert, Download, FileText, MessageSquare, Search } from "lucide-react";
import { errorText } from "@/api/client";
import { useFiles, useSession } from "@/api/hooks";
import { Notice, Page } from "@/app/Page";
import { Btn, inputCls } from "@/components/form";
import { Skeleton } from "@/components/ui/skeleton";
import { t } from "@/lib/copy";
import { attachmentHref, formatClock, formatDay, formatSize } from "@/lib/format";
import { cn } from "@/lib/utils";

export function FilesPage() {
  const { space } = useSession();
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  useEffect(() => {
    const id = setTimeout(() => setQ(text.trim()), 300);
    return () => clearTimeout(id);
  }, [text]);
  const files = useFiles(q);
  const list = (files.data?.pages ?? []).flatMap((p) => p.files ?? []);

  return (
    <Page title={t("files.title")} wide>
      <div className="mb-4 max-w-sm">
        <label htmlFor="files-q" className="sr-only">
          {t("files.search")}
        </label>
        <div className="relative">
          <Search
            aria-hidden
            className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-faint"
          />
          <input
            id="files-q"
            type="search"
            value={text}
            spellCheck={false}
            placeholder={t("files.placeholder")}
            onChange={(e) => setText(e.target.value)}
            className={cn(inputCls, "pl-8")}
          />
        </div>
      </div>
      {files.isPending && (
        <div className="space-y-2" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-11 bg-surface-sunk" />
          ))}
        </div>
      )}
      {files.isError && (
        <Notice icon={CircleAlert} tone="danger" title={t("files.loadFailed")}>
          {errorText(files.error)}
        </Notice>
      )}
      {files.isSuccess && list.length === 0 && (
        <div role="status">
          {q ? (
            <Notice icon={Search} title={t("files.noMatch.title", { q })}>
              {t("files.noMatch.body")}
            </Notice>
          ) : (
            <Notice icon={FileText} title={t("files.empty.title")}>
              {t("files.empty.body")}
            </Notice>
          )}
        </div>
      )}
      {list.length > 0 && (
        <ul
          aria-label={t("files.list")}
          className={cn("border-t border-rule-strong", files.isFetching && !files.isFetchingNextPage && "opacity-70")}
        >
          {list.map((f) => (
            <li
              key={f.id}
              className="grid grid-cols-[auto_1fr_auto] items-center gap-x-3 border-b border-rule px-1 py-2 sm:px-2"
            >
              <FileText aria-hidden className="size-4 shrink-0 text-ink-faint" />
              <div className="min-w-0">
                <a
                  href={attachmentHref(space.id, f.chat_id, f.id)}
                  download={f.name}
                  aria-label={t("chat.download", { name: f.name })}
                  className="group inline-flex max-w-full items-center gap-1.5 text-sm font-[var(--weight-medium)] text-ink hover:underline hover:underline-offset-2"
                >
                  <span className="truncate">{f.name}</span>
                  <Download aria-hidden className="size-3.5 shrink-0 text-ink-faint group-hover:text-ink" />
                </a>
                <p className="flex flex-wrap gap-x-1.5 text-xs text-ink-faint">
                  <span>{t("files.from", { bot: f.bot_name })}</span>
                  <span aria-hidden>·</span>
                  <time dateTime={f.sent_at}>
                    {formatDay(f.sent_at)} {formatClock(f.sent_at)}
                  </time>
                  <span aria-hidden>·</span>
                  <span className="figures">{formatSize(f.size)}</span>
                </p>
              </div>
              <Link
                to="/chat/$chatId"
                params={{ chatId: f.chat_id }}
                aria-label={t("files.openChat", { name: f.name })}
                title={t("files.openChat", { name: f.name })}
                className="inline-flex size-8 items-center justify-center rounded-sm text-ink-faint hover:bg-surface-sunk hover:text-ink"
              >
                <MessageSquare aria-hidden className="size-4" />
              </Link>
            </li>
          ))}
        </ul>
      )}
      {files.hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Btn size="sm" busy={files.isFetchingNextPage} onClick={() => void files.fetchNextPage()}>
            {t("files.more")}
          </Btn>
        </div>
      )}
    </Page>
  );
}
