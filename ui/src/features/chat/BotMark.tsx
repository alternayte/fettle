import { cn } from "@/lib/utils";

/** BotMark is a square monogram, stamped like a potter's mark on the foot of a pot. */
export function BotMark({
  name,
  size = "md",
  className,
}: {
  name: string;
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  const letters = name
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return (
    <span
      aria-hidden
      className={cn(
        "inline-flex shrink-0 select-none items-center justify-center rounded-xs border border-rule-strong bg-surface-sunk font-serif font-[var(--weight-medium)] text-ink-soft",
        size === "sm" && "size-5 text-2xs",
        size === "md" && "size-7 text-xs",
        size === "lg" && "size-10 text-md",
        className,
      )}
    >
      {letters}
    </span>
  );
}
