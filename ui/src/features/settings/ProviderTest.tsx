import { useState } from "react";
import { FlaskConical } from "lucide-react";
import { errorText } from "@/api/client";
import { usePresets, useTestProvider } from "@/api/hooks";
import type { SchemaProviderView } from "@/api/schema";
import { useMutationState } from "@tanstack/react-query";
import { Btn, CheckList } from "@/components/form";
import { t } from "@/lib/copy";
import { formatClock, formatDay } from "@/lib/format";
import { ModelPicker } from "./ModelPicker";
import { checkRows } from "./models";

/** useDefaultTestModel picks the last tested model, the preset suggestion or the first listed model. */
function useDefaultTestModel(p: SchemaProviderView): string {
  const presets = usePresets();
  const preset = presets.data?.find((x) => x.id === p.preset);
  return p.smoke.model || preset?.default_model || p.models?.[0]?.id || "";
}

/** ProviderTest runs the four checks against one model of a provider and shows each result. */
export function ProviderTest({
  provider,
  idPrefix,
  onTested,
}: {
  provider: SchemaProviderView;
  idPrefix: string;
  onTested?: (p: SchemaProviderView) => void;
}) {
  const fallback = useDefaultTestModel(provider);
  const [model, setModel] = useState("");
  const test = useTestProvider();
  const chosen = model || fallback;
  const pending = useMutationState({
    filters: { mutationKey: ["testProvider"], status: "pending" },
    select: (m) => (m.state.variables as { id: string } | undefined)?.id,
  });
  const running = pending.includes(provider.id);
  const run = () => test.mutate({ id: provider.id, model: chosen || undefined }, { onSuccess: (p) => onTested?.(p) });
  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
        <div className="min-w-0 flex-1 space-y-1">
          <label htmlFor={`${idPrefix}-model`} className="block text-sm font-[var(--weight-medium)] text-ink">
            {t("models.test.model")}
          </label>
          <ModelPicker id={`${idPrefix}-model`} models={provider.models ?? []} value={chosen} onChange={setModel} />
        </div>
        <Btn onClick={run} busy={running}>
          <FlaskConical aria-hidden className="size-3.5" />
          {t("models.test.run")}
        </Btn>
      </div>
      <CheckList checks={checkRows(provider, running)} label={t("setup.model.checks")} />
      <p className="figures text-ink-faint" aria-live="polite">
        {test.isError
          ? t("models.test.failed", { error: errorText(test.error) })
          : provider.tested_at
            ? t("models.test.at", {
                model: provider.smoke.model,
                when: `${formatDay(provider.tested_at)} ${formatClock(provider.tested_at)}`,
              })
            : t("models.test.never")}
      </p>
    </div>
  );
}
