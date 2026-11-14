import { ChatHeader } from "@/features/chat/ChatView";
import { cn } from "@/lib/utils";
import { useShell } from "./shell-context";

/** Page is a scrolling main column with the same header bar as the chat. */
export function Page({
  title,
  subtitle,
  actions,
  children,
  wide,
}: {
  title: string;
  subtitle?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
  wide?: boolean;
}) {
  const shell = useShell();
  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col">
      <ChatHeader title={title} subtitle={subtitle} onMenu={shell.openMenu}>
        {actions}
      </ChatHeader>
      <div className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className={cn("mx-auto w-full px-4 pt-5 pb-12 sm:px-8", wide ? "max-w-5xl" : "max-w-3xl")}>{children}</div>
      </div>
    </section>
  );
}

/** Section is one ruled part of a page, like a page of the log book. */
export function Section({
  id,
  title,
  aside,
  children,
  className,
}: {
  id?: string;
  title: string;
  aside?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section id={id} aria-labelledby={id ? `${id}-title` : undefined} className={cn("mt-8 first:mt-0", className)}>
      <div className="mb-3 flex items-end gap-3 border-b border-rule-strong pb-1.5">
        <h2 id={id ? `${id}-title` : undefined} className="serif-title text-xl text-ink">
          {title}
        </h2>
        {aside && <div className="ml-auto flex items-center gap-2">{aside}</div>}
      </div>
      {children}
    </section>
  );
}

/** Notice is a calm inline message for empty, loading and error states. */
export function Notice({
  icon: Icon,
  title,
  children,
  tone = "plain",
}: {
  icon?: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  title: string;
  children?: React.ReactNode;
  tone?: "plain" | "danger";
}) {
  return (
    <div
      role={tone === "danger" ? "alert" : undefined}
      className={cn(
        "flex items-start gap-2.5 rounded-sm border px-3 py-2.5 text-sm",
        tone === "danger" ? "border-danger/40 bg-danger-wash text-ink" : "border-rule bg-surface text-ink-soft",
      )}
    >
      {Icon && (
        <Icon
          aria-hidden
          className={cn("mt-0.5 size-4 shrink-0", tone === "danger" ? "text-danger" : "text-ink-faint")}
        />
      )}
      <div className="min-w-0">
        <p className="font-[var(--weight-medium)] text-ink">{title}</p>
        {children && <div className="mt-0.5 break-words">{children}</div>}
      </div>
    </div>
  );
}
