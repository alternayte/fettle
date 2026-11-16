import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { t } from "@/lib/copy";

function ptyURL(token: string, session: string, cols: number, rows: number): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const q = new URLSearchParams({ t: token, s: session, cols: String(cols), rows: String(rows) });
  return `${proto}//${window.location.host}/api/v1/teach/pty?${q.toString()}`;
}

/**
 * TeachTerminal is the terminal pane of the teach view. Fettle records each command that
 * runs in it, with its folder, exit code and the first 2 KB of output. Input typed while the
 * terminal hides it, as at a password prompt, is never recorded.
 */
export function TeachTerminal({ token, session }: { token: string; session: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<"connecting" | "open" | "closed">("connecting");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!ref.current) return;
    // xterm draws on a canvas, so it takes the theme tokens as values.
    const style = getComputedStyle(document.documentElement);
    const cssVar = (name: string) => style.getPropertyValue(name).trim();
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: cssVar("--font-mono") || "monospace",
      fontSize: 13,
      scrollback: 2000,
      theme: {
        background: cssVar("--surface-sunk"),
        foreground: cssVar("--ink"),
        cursor: cssVar("--accent"),
        selectionBackground: cssVar("--accent-wash"),
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(ref.current);
    fit.fit();
    const ws = new WebSocket(ptyURL(token, session, term.cols, term.rows));
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();
    // The terminal does not take the focus when it connects: the person may be on the screen.
    ws.onopen = () => setState("open");
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
      else term.write(String(e.data));
    };
    ws.onclose = () => setState("closed");
    const input = term.onData((d) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });
    const resize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ cols, rows }));
    });
    const obs = new ResizeObserver(() => fit.fit());
    obs.observe(ref.current);
    return () => {
      obs.disconnect();
      input.dispose();
      resize.dispose();
      ws.close();
      term.dispose();
    };
  }, [token, session, attempt]);
  return (
    <div className="relative h-full min-h-0 bg-surface-sunk">
      <div ref={ref} role="region" aria-label={t("teach.terminal")} className="h-full w-full p-1.5" />
      {state !== "open" && (
        <div className="absolute inset-0 flex items-center justify-center bg-surface-sunk/85 text-sm text-ink-soft">
          {state === "connecting" ? (
            <span role="status">{t("teach.terminalConnecting")}</span>
          ) : (
            <button type="button" className="underline underline-offset-2" onClick={() => setAttempt((n) => n + 1)}>
              {t("teach.terminalReconnect")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
