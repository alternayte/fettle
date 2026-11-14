import { useEffect, useState } from "react";
import { BellOff, BellRing } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { useSession, useSetSetting, useSettings } from "@/api/hooks";
import { Notice, Section } from "@/app/Page";
import { Btn } from "@/components/form";
import { Switch } from "@/components/ui/switch";
import { t } from "@/lib/copy";
import { devicePushOn, disablePush, enablePush, pushSupported } from "@/lib/push";

export function NotificationsPage() {
  const { isOwner } = useSession();
  const [on, setOn] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const settings = useSettings();
  const setSetting = useSetSetting();
  const finished = settings.data?.find((s) => s.key === "push.finished")?.value === true;
  useEffect(() => {
    void devicePushOn().then(setOn);
  }, []);
  const supported = pushSupported() && window.isSecureContext;
  return (
    <div>
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("notify.intro")}</p>
      <Section id="device" title={t("notify.device")}>
        {!supported ? (
          <Notice icon={BellOff} title={t("notify.unsupported.title")}>
            {t("notify.unsupported.body")}
          </Notice>
        ) : (
          <div className="flex flex-wrap items-center gap-3 border-y border-rule py-3">
            {on ? (
              <BellRing aria-hidden className="size-4 text-ok" />
            ) : (
              <BellOff aria-hidden className="size-4 text-ink-faint" />
            )}
            <span className="text-sm text-ink">{on ? t("notify.on") : t("notify.off")}</span>
            <Btn
              size="sm"
              className="ml-auto"
              busy={busy || on === null}
              onClick={async () => {
                setBusy(true);
                try {
                  if (on) {
                    await disablePush();
                    setOn(false);
                  } else if ((await enablePush()) === "denied") {
                    toast.error(t("notify.denied"));
                  } else {
                    setOn(true);
                  }
                } catch (err) {
                  toast.error(t("notify.failed"), { description: errorText(err) });
                } finally {
                  setBusy(false);
                }
              }}
            >
              {on ? t("notify.turnOff") : t("notify.turnOn")}
            </Btn>
          </div>
        )}
        <p className="mt-2 text-xs text-ink-faint">{t("notify.types")}</p>
      </Section>
      {isOwner && (
        <Section id="finished" title={t("notify.finishedTitle")}>
          <label className="flex items-center gap-3 border-y border-rule py-3 text-sm text-ink">
            <Switch
              checked={finished}
              disabled={setSetting.isPending}
              onCheckedChange={(v) => setSetting.mutate({ key: "push.finished", value: v })}
            />
            {t("notify.finished")}
          </label>
        </Section>
      )}
    </div>
  );
}
