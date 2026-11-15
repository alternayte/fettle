import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { ArrowUp, CornerUpLeft, Loader, Paperclip, Square, X } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import type { Attachment, Bot, Message } from "./types";

type Pending = { key: string; name: string; at?: Attachment };

/** mentionAt returns the @word that ends at the caret, if any. */
function mentionAt(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret);
  const m = /(^|[\s(])@([\p{L}\p{N} _-]{0,40})$/u.exec(before);
  if (!m) return null;
  return { start: caret - m[2].length - 1, query: m[2] };
}

export function Composer({
  botName,
  running,
  onSend,
  onStop,
  disabled,
  onUpload,
  members,
  replyTo,
  replyName,
  onCancelReply,
}: {
  botName: string;
  running: boolean;
  onSend: (text: string, attachments: Attachment[]) => void;
  onStop?: () => void;
  disabled?: boolean;
  /** Uploads one file. Without it the composer has no attach action. */
  onUpload?: (file: File) => Promise<Attachment>;
  /** The bots of a group chat, for @mentions. */
  members?: Bot[];
  replyTo?: Message;
  replyName?: string;
  onCancelReply?: () => void;
}) {
  const [text, setText] = useState("");
  const [caret, setCaret] = useState(0);
  const [files, setFiles] = useState<Pending[]>([]);
  const [pick, setPick] = useState(0);
  const ref = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
  }, [text]);

  const mention = members && members.length > 0 ? mentionAt(text, caret) : null;
  const options = useMemo(() => {
    if (!mention || !members) return [];
    const q = mention.query.toLowerCase();
    const list = members
      .filter((b) => b.name.toLowerCase().startsWith(q))
      .map((b) => b.name)
      .slice(0, 6);
    if ("everyone".startsWith(q)) list.push("everyone");
    return list;
  }, [mention, members]);

  const choose = (name: string) => {
    if (!mention) return;
    const next = text.slice(0, mention.start) + "@" + name + " " + text.slice(caret);
    const pos = mention.start + name.length + 2;
    setText(next);
    setPick(0);
    requestAnimationFrame(() => {
      ref.current?.setSelectionRange(pos, pos);
      setCaret(pos);
    });
  };

  const uploading = files.some((f) => !f.at);
  const send = () => {
    const v = text.trim();
    const atts = files.flatMap((f) => (f.at ? [f.at] : []));
    if ((!v && atts.length === 0) || disabled || uploading) return;
    onSend(v, atts);
    setText("");
    setFiles([]);
  };

  const attach = (list: FileList | null) => {
    if (!list || !onUpload) return;
    for (const file of Array.from(list)) {
      const key = `${file.name}-${Math.random().toString(36).slice(2)}`;
      setFiles((fs) => [...fs, { key, name: file.name }]);
      onUpload(file).then(
        (at) => setFiles((fs) => fs.map((f) => (f.key === key ? { ...f, at } : f))),
        (err) => {
          setFiles((fs) => fs.filter((f) => f.key !== key));
          toast.error(t("chat.uploadFailed", { name: file.name }), { description: errorText(err) });
        },
      );
    }
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
      className="relative mx-auto w-full max-w-[calc(var(--measure)+5rem)] px-3 pb-3 sm:px-6 sm:pb-4"
    >
      {options.length > 0 && (
        <ul
          role="listbox"
          aria-label={t("chat.mentions")}
          className="absolute bottom-full left-3 z-10 mb-1 min-w-48 rounded-sm border border-rule-strong bg-surface-raised py-1 shadow-[var(--shadow-2)] sm:left-6"
        >
          {options.map((n, i) => (
            <li key={n} role="option" aria-selected={i === pick}>
              <button
                type="button"
                onMouseDown={(e) => {
                  e.preventDefault();
                  choose(n);
                }}
                className={cn("block w-full px-3 py-1 text-left text-sm text-ink", i === pick && "bg-surface-sunk")}
              >
                @{n}
              </button>
            </li>
          ))}
        </ul>
      )}
      <div
        className={cn(
          "rounded-md border border-rule-strong bg-surface-raised shadow-[var(--shadow-1)]",
          "transition-[border-color,box-shadow] duration-[var(--dur-2)] focus-within:border-ink-faint focus-within:shadow-[var(--shadow-2)]",
        )}
      >
        {replyTo && (
          <div className="flex items-center gap-2 border-b border-rule px-3 py-1.5 text-xs text-ink-soft">
            <CornerUpLeft aria-hidden className="size-3.5 shrink-0" />
            <span className="shrink-0 font-[var(--weight-medium)] text-ink">
              {t("chat.replyingTo", { name: replyName ?? "" })}
            </span>
            <span className="min-w-0 truncate">{replyTo.body}</span>
            <button
              type="button"
              onClick={onCancelReply}
              aria-label={t("chat.cancelReply")}
              className="ml-auto inline-flex size-6 shrink-0 items-center justify-center rounded-sm hover:bg-surface-sunk hover:text-ink"
            >
              <X aria-hidden className="size-3.5" />
            </button>
          </div>
        )}
        <label htmlFor="composer" className="sr-only">
          {t("chat.composer.label", { name: botName })}
        </label>
        <textarea
          id="composer"
          ref={ref}
          value={text}
          rows={1}
          disabled={disabled}
          onChange={(e) => {
            setText(e.target.value);
            setCaret(e.target.selectionStart ?? e.target.value.length);
            setPick(0);
          }}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart ?? 0)}
          onKeyDown={(e) => {
            if (options.length > 0) {
              if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                e.preventDefault();
                setPick((p) => (p + (e.key === "ArrowDown" ? 1 : options.length - 1)) % options.length);
                return;
              }
              if (e.key === "Enter" || e.key === "Tab") {
                e.preventDefault();
                choose(options[pick] ?? options[0]);
                return;
              }
            }
            if (e.key === "Escape" && replyTo) onCancelReply?.();
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              send();
            }
          }}
          placeholder={
            running ? t("chat.composer.steer", { name: botName }) : t("chat.composer.placeholder", { name: botName })
          }
          className="block max-h-60 min-h-11 w-full resize-none bg-transparent px-3.5 pt-3 pb-1 text-base text-ink outline-none placeholder:text-ink-faint focus-visible:outline-none"
        />
        {files.length > 0 && (
          <ul className="flex flex-wrap gap-1.5 px-3 pb-1.5">
            {files.map((f) => (
              <li
                key={f.key}
                className="inline-flex items-center gap-1.5 rounded-xs border border-rule bg-surface px-2 py-1 text-xs text-ink-soft"
              >
                {f.at ? (
                  <Paperclip aria-hidden className="size-3" />
                ) : (
                  <Loader aria-label={t("chat.uploading", { name: f.name })} className="size-3 animate-spin" />
                )}
                <span className="max-w-48 truncate text-ink">{f.name}</span>
                <button
                  type="button"
                  onClick={() => setFiles((fs) => fs.filter((x) => x.key !== f.key))}
                  aria-label={t("chat.removeFile", { name: f.name })}
                  className="inline-flex size-4 items-center justify-center rounded-xs hover:text-ink"
                >
                  <X aria-hidden className="size-3" />
                </button>
              </li>
            ))}
          </ul>
        )}
        <div className="flex items-center gap-1 px-2 pb-2">
          {onUpload && (
            <>
              <input
                ref={fileRef}
                type="file"
                multiple
                className="hidden"
                tabIndex={-1}
                aria-hidden
                onChange={(e) => {
                  attach(e.target.files);
                  e.target.value = "";
                }}
              />
              <button
                type="button"
                onClick={() => fileRef.current?.click()}
                aria-label={t("chat.composer.attach")}
                className="inline-flex size-7 items-center justify-center rounded-sm text-ink-faint hover:bg-surface-sunk hover:text-ink"
              >
                <Paperclip className="size-4" aria-hidden />
              </button>
            </>
          )}
          <span className="figures ml-1 hidden text-ink-faint sm:inline">{t("chat.composer.hint")}</span>
          <div className="ml-auto flex items-center gap-1">
            {running && onStop && (
              <button
                type="button"
                onClick={onStop}
                className="inline-flex h-7 items-center gap-1.5 rounded-sm border border-rule-strong px-2 text-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
              >
                <Square aria-hidden className="size-3 fill-current" />
                {t("chat.composer.stop")}
              </button>
            )}
            <button
              type="submit"
              aria-label={t("chat.composer.send")}
              disabled={(!text.trim() && files.length === 0) || disabled || uploading}
              className="inline-flex size-7 items-center justify-center rounded-sm bg-ink text-surface transition-opacity duration-[var(--dur-1)] disabled:opacity-30"
            >
              <ArrowUp className="size-4" aria-hidden />
            </button>
          </div>
        </div>
      </div>
    </form>
  );
}
