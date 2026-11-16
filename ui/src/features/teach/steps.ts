import type { SchemaTeachStepView } from "@/api/schema";
import { t } from "@/lib/copy";

/** stepText is one line about a recorded step: the role and name of a browser target, or the command. */
export function stepText(s: SchemaTeachStepView): string {
  if (s.kind === "note") return (s.notes ?? [])[0] ?? "";
  if (s.kind === "command") return s.command ?? "";
  if (s.action === "navigate") return s.url ?? "";
  const target = `${s.role ?? ""} “${s.name ?? ""}”`.trim();
  if (s.secret) return t("teach.step.secret", { target, kind: t(`teach.secret.${s.secret}`) });
  if (s.action === "type" || s.action === "select") return t("teach.step.value", { target, value: s.value ?? "" });
  return target;
}
