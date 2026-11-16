import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { GraduationCap, Monitor } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useStartTeach, useTeachReady } from "@/api/hooks";
import { Btn, Field, inputCls } from "@/components/form";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { t } from "@/lib/copy";

/**
 * TeachDialog starts a teach session for a bot (§13.3). Teaching needs the desktop size; for
 * a zone with another size, it offers the change to an owner before the session starts.
 */
export function TeachDialog({
  botId,
  botName,
  takeoverCard,
  open,
  onOpenChange,
}: {
  botId: string;
  botName: string;
  takeoverCard?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const ready = useTeachReady(botId, open);
  const start = useStartTeach();
  const navigate = useNavigate();
  const [title, setTitle] = useState("");
  const r = ready.data;
  const needsSize = !!r && !!r.zone_id && !r.ready;
  const go = (changeSize: boolean) =>
    start.mutate(
      {
        bot_id: botId,
        title: title.trim() || undefined,
        takeover_card: takeoverCard,
        change_size: changeSize || undefined,
      },
      {
        onSuccess: (res) => {
          onOpenChange(false);
          void navigate({ to: "/teach/$teachId", params: { teachId: res.session.id } });
        },
        onError: (err) => toast.error(t("teach.startFailed"), { description: errorText(err) }),
      },
    );
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="border-rule-strong bg-surface-raised text-ink">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <GraduationCap aria-hidden className="size-4 text-glaze" />
            {t("teach.dialogTitle", { bot: botName })}
          </DialogTitle>
          <DialogDescription className="text-sm text-ink-soft">{t("teach.dialogBody")}</DialogDescription>
        </DialogHeader>
        {ready.isPending && (
          <p role="status" className="text-sm text-ink-faint">
            {t("teach.checking")}
          </p>
        )}
        {ready.isError && (
          <p role="alert" className="text-sm text-danger">
            {errorText(ready.error)}
          </p>
        )}
        {r && !r.zone_id && <p className="text-sm text-ink-soft">{t("teach.noZone", { bot: botName })}</p>}
        {r?.session_id && (
          <p className="text-sm text-ink-soft">
            {t("teach.resume")}{" "}
            <button
              type="button"
              className="text-ink underline underline-offset-2"
              onClick={() => {
                onOpenChange(false);
                void navigate({ to: "/teach/$teachId", params: { teachId: r.session_id! } });
              }}
            >
              {t("teach.resumeOpen")}
            </button>
          </p>
        )}
        {r?.zone_id && !r.session_id && (
          <>
            {needsSize && (
              <div
                role="note"
                className="flex items-start gap-2 rounded-sm border border-glaze/40 bg-surface px-3 py-2 text-sm text-ink"
              >
                <Monitor aria-hidden className="mt-0.5 size-4 shrink-0 text-glaze" />
                <p>
                  {t("teach.needsDesktop", { zone: r.zone_name ?? "", size: r.size ?? "" })}{" "}
                  {r.can_change ? t("teach.offerChange") : t("teach.askOwner", { zone: r.zone_name ?? "" })}
                </p>
              </div>
            )}
            <Field id="teach-title" label={t("teach.taskLabel")} hint={t("teach.taskHint")}>
              <input
                id="teach-title"
                value={title}
                maxLength={300}
                placeholder={t("teach.taskPlaceholder")}
                onChange={(e) => setTitle(e.target.value)}
                className={inputCls}
              />
            </Field>
          </>
        )}
        <DialogFooter className="gap-2">
          <Btn kind="quiet" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Btn>
          {r?.zone_id && !r.session_id && (!needsSize || r.can_change) && (
            <Btn kind="act" busy={start.isPending} onClick={() => go(needsSize)}>
              {needsSize ? t("teach.changeAndStart") : t("teach.start")}
            </Btn>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
