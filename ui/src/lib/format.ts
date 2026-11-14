import { t } from "@/lib/copy";

export function formatUSD(v: number): string {
  if (v === 0) return "$0";
  if (v < 0.01) return "<$0.01";
  return `$${v.toFixed(2)}`;
}

export function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return t("units.seconds", { n: s });
  const m = Math.round(s / 60);
  if (m < 60) return t("units.minutes", { n: m });
  return t("units.hours", { h: Math.floor(m / 60), m: m % 60 });
}

export function formatClock(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function formatClockPrecise(iso: string): string {
  const d = new Date(iso);
  const hms = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  return `${hms}.${String(d.getMilliseconds()).padStart(3, "0")}`;
}

export function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  return `${(n / 1000).toFixed(n < 10000 ? 1 : 0)}k`;
}

export function formatDay(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  const y = new Date(today);
  y.setDate(today.getDate() - 1);
  if (d.toDateString() === today.toDateString()) return t("units.today");
  if (d.toDateString() === y.toDateString()) return t("units.yesterday");
  return d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" });
}
