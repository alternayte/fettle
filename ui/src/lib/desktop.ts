import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";

/**
 * useDesktopLinks keeps the desktop window on the Fettle UI origin (SI-16): a link to
 * another origin opens in the system browser, and a bot's screen opens in a window of its
 * own (C-06). Outside the desktop app it does nothing.
 */
export function useDesktopLinks() {
  const instance = useQuery({ queryKey: ["instance"], queryFn: async () => (await api.GET("/api/v1/instance")).data });
  const desktop = !!instance.data?.desktop;
  useEffect(() => {
    if (!desktop) return;
    const onClick = (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0) return;
      const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!a) return;
      const url = new URL(a.href, window.location.href);
      if (url.origin !== window.location.origin) {
        e.preventDefault();
        e.stopPropagation();
        void api.POST("/api/v1/desktop/open", { body: { url: url.href } });
        return;
      }
      if (url.pathname.startsWith("/screen/")) {
        e.preventDefault();
        e.stopPropagation();
        void api.POST("/api/v1/desktop/window", {
          body: { path: url.pathname + url.search, title: a.textContent ?? "" },
        });
      }
    };
    document.addEventListener("click", onClick, true);
    const open = window.open;
    window.open = (u?: string | URL, ...rest) => {
      if (u) {
        const url = new URL(String(u), window.location.href);
        if (url.origin !== window.location.origin) {
          void api.POST("/api/v1/desktop/open", { body: { url: url.href } });
          return null;
        }
      }
      return open.call(window, u, ...rest);
    };
    return () => {
      document.removeEventListener("click", onClick, true);
      window.open = open;
    };
  }, [desktop]);
}
