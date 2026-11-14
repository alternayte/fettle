import { useEffect, useRef } from "react";

export type StreamHandlers = Record<string, (data: unknown) => void>;

/**
 * useEventStream keeps one EventSource open to url. The browser reconnects after a network
 * error. When the server closes the stream, the hook opens a new one with backoff.
 * onReconnect runs after each open except the first, so the caller can refetch what it missed.
 */
export function useEventStream(url: string | null, handlers: StreamHandlers, onReconnect?: () => void) {
  const h = useRef(handlers);
  const r = useRef(onReconnect);
  useEffect(() => {
    h.current = handlers;
    r.current = onReconnect;
  });

  useEffect(() => {
    if (!url) return;
    let es: EventSource | null = null;
    let opened = 0;
    let retry = 1000;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let closed = false;
    const names = Object.keys(h.current);

    const connect = () => {
      es = new EventSource(url, { withCredentials: true });
      es.onopen = () => {
        retry = 1000;
        if (opened++ > 0) r.current?.();
      };
      es.onerror = () => {
        if (!es || es.readyState !== EventSource.CLOSED || closed) return;
        es = null;
        timer = setTimeout(connect, retry);
        retry = Math.min(retry * 2, 15000);
        opened = Math.max(opened, 1);
      };
      for (const name of names) {
        es.addEventListener(name, (ev) => {
          let data: unknown;
          try {
            data = JSON.parse((ev as MessageEvent<string>).data);
          } catch {
            return;
          }
          h.current[name]?.(data);
        });
      }
    };
    connect();
    return () => {
      closed = true;
      clearTimeout(timer);
      es?.close();
    };
  }, [url]);
}
