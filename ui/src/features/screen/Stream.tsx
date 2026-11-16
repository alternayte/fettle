import { useEffect, useRef } from "react";
import RFB from "@novnc/novnc/lib/rfb";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

export type Mode = { token: string; control: boolean };

function streamURL(token: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/screen/stream?t=${encodeURIComponent(token)}`;
}

/** Stream shows one VNC stream. A new mode makes a new connection. */
export function Stream({ mode, onLost }: { mode: Mode; onLost: () => void }) {
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
    // Keys go to the screen after a click on it, also when another pane, such as the teach
    // terminal, had the focus.
    const el = ref.current;
    const take = () => {
      if (mode.control) rfb.focus();
    };
    el.addEventListener("pointerdown", take, true);
    return () => {
      closing = true;
      rfb.removeEventListener("disconnect", lost);
      el.removeEventListener("pointerdown", take, true);
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
