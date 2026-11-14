import type { SchemaProviderView, SchemaTarget } from "@/api/schema";
import { CHECK_KEYS, type CheckRow } from "@/components/form";

export function checkRows(p: SchemaProviderView | undefined, running: boolean): CheckRow[] {
  return CHECK_KEYS.map((key) => {
    if (running) return { key, state: "running" };
    const c = p?.smoke.checks?.find((x) => x.key === key);
    if (!c || !p?.tested_at) return { key, state: "idle" };
    return { key, state: c.pass ? "pass" : "fail", detail: c.detail || undefined, ms: c.ms };
  });
}

export function targetFor(p: SchemaProviderView, model: string): SchemaTarget {
  const info = p.models?.find((m) => m.id === model);
  return {
    provider_id: p.id,
    model,
    price_in: info?.price_in ?? 0,
    price_out: info?.price_out ?? 0,
    context_window: info?.context_window ?? 0,
  };
}
