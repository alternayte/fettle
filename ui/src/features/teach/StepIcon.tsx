import { KeyRound, MousePointerClick, SquareTerminal, StickyNote } from "lucide-react";
import type { SchemaTeachStepView } from "@/api/schema";
import { cn } from "@/lib/utils";

/** StepIcon marks the kind of a recorded step; a login step has a key. */
export function StepIcon({ s }: { s: SchemaTeachStepView }) {
  const Icon = s.secret
    ? KeyRound
    : s.kind === "command"
      ? SquareTerminal
      : s.kind === "note"
        ? StickyNote
        : MousePointerClick;
  return <Icon aria-hidden className={cn("mt-0.5 size-3.5 shrink-0", s.secret ? "text-warn" : "text-ink-faint")} />;
}
