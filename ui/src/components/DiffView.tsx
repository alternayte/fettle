import { cn } from "@/lib/utils";

/** DiffView shows a unified diff with added and removed lines in color. */
export function DiffView({ diff, label, className }: { diff: string; label: string; className?: string }) {
  return (
    <pre
      aria-label={label}
      className={cn(
        "max-h-72 overflow-auto rounded-xs border border-rule bg-surface-sunk p-2 font-mono text-xs leading-relaxed",
        className,
      )}
    >
      {diff.split("\n").map((line, i) => (
        <div
          key={i}
          className={cn(
            line.startsWith("+") && !line.startsWith("+++") && "text-ok",
            line.startsWith("-") && !line.startsWith("---") && "text-danger",
            (line.startsWith("@@") || line.startsWith("---") || line.startsWith("+++")) && "text-ink-faint",
          )}
        >
          {line || " "}
        </div>
      ))}
    </pre>
  );
}
