import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { CircleAlert, Hand, Monitor, X } from "lucide-react";
import { toast } from "sonner";
import RFB from "@novnc/novnc/lib/rfb";
import { errorText } from "@/api/client";
import { useBot, useControlScreen, useOpenScreen, useScreenDone } from "@/api/hooks";
import { Btn, inputCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

type Mode = { token: string; control: boolean };

function streamURL(token: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/screen/stream?t=${encodeURIComponent(token)}`;
}

/** Stream shows one VNC stream. A new mode makes a new connection. */
function Stream({ mode, onLost }: { mode: Mode; onLost: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!ref.current) return;
    const rfb = new RFB(ref.current, streamURL(mode.token), { wsProtocols: ["binary"], shared: true });
    rfb.viewOnly = !mode.control;
    rfb.scaleViewport = true;
    rfb.resizeSession = false;
    rfb.background = "transparent";
    let closing = false;
    const lost = () => {
      if (!closing) onLost();
    };
    rfb.addEventListener("disconnect", lost);
    if (mode.control) rfb.focus();
    return () => {
      closing = true;
      rfb.removeEventListener("disconnect", lost);
      rfb.disconnect();
    };
  }, [mode, onLost]);
  return (
    <div
      ref={ref}
      role="img"
      aria-label={mode.control ? t("screen.youControl") : t("screen.viewOnly")}
      className={cn("h-full w-full", mode.control && "outline-2 outline-offset-[-2px] outline-glaze")}
    />
  );
}

/** ScreenPage is the full-screen panel of a bot's screen (§10.8). */
export function ScreenPage() {
  const { botId } = useParams({ strict: false }) as { botId: string };
  const search = useSearch({ strict: false }) as { card?: string };
  const navigate = useNavigate();
  const bot = useBot(botId);
  const open = useOpenScreen();
  const control = useControlScreen();
  const done = useScreenDone();
  const [mode, setMode] = useState<Mode | null>(null);
  const [controller, setController] = useState<string>("none");
  const [lost, setLost] = useState(false);
  const [note, setNote] = useState("");
  const name = bot.data?.name ?? "";

  const view = useCallback(() => {
    setLost(false);
    open.mutate(botId, {
      onSuccess: (s) => {
        setController(s.controller);
        setMode({ token: s.view_token, control: false });
      },
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [botId]);
  useEffect(view, [view]);

  // The control lease lasts 15 minutes; renew it while the panel is open.
  const controlling = mode?.control ?? false;
  useEffect(() => {
    if (!controlling) return;
    const id = window.setInterval(() => control.mutate(botId), 10 * 60 * 1000);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controlling, botId]);

  const take = () =>
    control.mutate(botId, {
      onSuccess: (c) => {
        setLost(false);
        setController("you");
        setMode({ token: c.token, control: true });
      },
      onError: (err) => toast.error(errorText(err)),
    });

  const back = () => {
    if (window.history.length > 1) window.history.back();
    else void navigate({ to: "/" });
  };
  const finish = () =>
    done.mutate(
      { bot: botId, card: search.card, note: note.trim() || undefined },
      {
        onSuccess: () => {
          toast.success(t("screen.returned", { bot: name }));
          back();
        },
        onError: (err) => toast.error(errorText(err)),
      },
    );
  const close = () => {
    // Closing the panel gives control back, so the bot does not wait for a lost lease.
    if (controlling) done.mutate({ bot: botId });
    back();
  };
  const onLost = useCallback(() => setLost(true), []);

  const status = controlling
    ? t("screen.youControl")
    : controller === "bot"
      ? t("screen.botUses", { bot: name })
      : controller === "person"
        ? t("screen.personUses")
        : t("screen.viewOnly");

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t("screen.title", { bot: name })}
      className="fixed inset-0 z-50 flex flex-col bg-ground"
    >
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-rule-strong px-3 py-2 sm:px-4">
        <Btn size="sm" kind="quiet" onClick={close} aria-label={t("screen.close")}>
          <X aria-hidden className="size-4" />
        </Btn>
        <Monitor aria-hidden className="size-4 text-ink-faint" />
        <h1 className="serif-title truncate text-lg text-ink">{t("screen.title", { bot: name })}</h1>
        <span
          role="status"
          className={cn(
            "label-caps rounded-sm border px-1.5 py-0.5",
            controlling ? "border-glaze/60 text-glaze" : "border-rule text-ink-faint",
          )}
        >
          {status}
        </span>
        <div className="flex w-full flex-wrap items-center gap-2 sm:ml-auto sm:w-auto">
          {!controlling && (
            <Btn size="sm" kind="act" busy={control.isPending} disabled={!mode} onClick={take}>
              <Hand aria-hidden className="size-3.5" />
              {t("screen.takeControl")}
            </Btn>
          )}
          {(controlling || search.card) && (
            <>
              {search.card && (
                <input
                  aria-label={t("screen.note")}
                  placeholder={t("screen.notePlaceholder")}
                  value={note}
                  maxLength={2000}
                  onChange={(e) => setNote(e.target.value)}
                  className={cn(inputCls, "h-7 min-w-0 flex-1 text-sm sm:w-72 sm:flex-none")}
                />
              )}
              <Btn size="sm" kind="primary" busy={done.isPending} onClick={finish} title={t("screen.doneHint")}>
                {t("screen.done")}
              </Btn>
            </>
          )}
        </div>
      </header>
      <main className="relative min-h-0 flex-1 bg-surface-sunk">
        {mode && !lost && <Stream key={mode.token} mode={mode} onLost={onLost} />}
        {(open.isPending || (!mode && !open.isError)) && (
          <p role="status" className="absolute inset-0 flex items-center justify-center text-sm text-ink-faint">
            {t("screen.connecting")}
          </p>
        )}
        {(open.isError || lost) && (
          <div className="absolute inset-0 flex items-center justify-center p-4">
            <div
              role="alert"
              className="flex max-w-md items-start gap-2.5 rounded-sm border border-danger/40 bg-surface px-4 py-3 text-sm text-ink"
            >
              <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-danger" />
              <div>
                <p className="font-[var(--weight-medium)]">{open.isError ? t("screen.failed") : t("screen.lost")}</p>
                {open.isError && <p className="mt-1 text-ink-soft">{errorText(open.error)}</p>}
                <Btn size="sm" className="mt-3" onClick={view}>
                  {t("screen.reconnect")}
                </Btn>
              </div>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}
