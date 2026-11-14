import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { t } from "@/lib/copy";

export function Shell({
  sidebar,
  children,
  panel,
  menuOpen,
  onMenuOpenChange,
}: {
  sidebar: React.ReactNode;
  children: React.ReactNode;
  panel: React.ReactNode | null;
  menuOpen: boolean;
  onMenuOpenChange: (open: boolean) => void;
}) {
  const reduce = useReducedMotion();
  return (
    <div className="flex h-dvh w-full overflow-hidden bg-ground">
      <aside className="hidden w-[var(--sidebar-w)] shrink-0 border-r border-rule md:block">{sidebar}</aside>
      <Sheet open={menuOpen} onOpenChange={onMenuOpenChange}>
        <SheetContent side="left" className="w-[min(85vw,var(--sidebar-w))] gap-0 border-rule bg-surface p-0 md:hidden">
          <SheetTitle className="sr-only">{t("shell.nav")}</SheetTitle>
          {sidebar}
        </SheetContent>
      </Sheet>
      <main className="flex min-w-0 flex-1">{children}</main>
      <AnimatePresence initial={false}>
        {panel && (
          <motion.aside
            key="panel"
            initial={reduce ? false : { opacity: 0, x: 24 }}
            animate={{ opacity: 1, x: 0 }}
            exit={reduce ? undefined : { opacity: 0, x: 24 }}
            transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
            aria-label={t("shell.panel")}
            className="fixed inset-0 z-40 flex min-h-0 flex-col bg-ground lg:static lg:z-auto lg:w-[var(--panel-w)] lg:shrink-0 lg:border-l lg:border-rule"
          >
            {panel}
          </motion.aside>
        )}
      </AnimatePresence>
    </div>
  );
}
