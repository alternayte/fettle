import { useLayoutEffect, useRef, useState } from "react";
import { ArrowUp, Paperclip, Square } from "lucide-react";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

export function Composer({
  botName,
  running,
  onSend,
  onStop,
  disabled,
}: {
  botName: string;
  running: boolean;
  onSend: (text: string) => void;
  onStop?: () => void;
  disabled?: boolean;
}) {
  const [text, setText] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
  }, [text]);

  const send = () => {
    const v = text.trim();
    if (!v || disabled) return;
    onSend(v);
    setText("");
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
      className="mx-auto w-full max-w-[calc(var(--measure)+5rem)] px-3 pb-3 sm:px-6 sm:pb-4"
    >
      <div
        className={cn(
          "rounded-md border border-rule-strong bg-surface-raised shadow-[var(--shadow-1)]",
          "transition-[border-color,box-shadow] duration-[var(--dur-2)] focus-within:border-ink-faint focus-within:shadow-[var(--shadow-2)]",
        )}
      >
        <label htmlFor="composer" className="sr-only">
          {t("chat.composer.label", { name: botName })}
        </label>
        <textarea
          id="composer"
          ref={ref}
          value={text}
          rows={1}
          disabled={disabled}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
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
        <div className="flex items-center gap-1 px-2 pb-2">
          <button
            type="button"
            aria-label={t("chat.composer.attach")}
            className="inline-flex size-7 items-center justify-center rounded-sm text-ink-faint hover:bg-surface-sunk hover:text-ink"
          >
            <Paperclip className="size-4" aria-hidden />
          </button>
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
              disabled={!text.trim() || disabled}
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
