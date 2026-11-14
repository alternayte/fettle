import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  keys,
  usePresets,
  useProviders,
  useSession,
  useSetRole,
  useTestProvider,
  useUpdateMe,
  useUpdateSpace,
} from "@/api/hooks";
import type { SchemaProviderView } from "@/api/schema";
import { Btn, Field, inputCls } from "@/components/form";
import { ModelPicker } from "@/features/settings/ModelPicker";
import { ProviderForm } from "@/features/settings/ProviderForm";
import { ProviderTest } from "@/features/settings/ProviderTest";
import { TimeZoneInput } from "@/features/settings/TimeZoneInput";
import { targetFor } from "@/features/settings/models";
import { t } from "@/lib/copy";
import { browserTimeZone } from "@/lib/tz";
import { WizardFrame } from "./Wizard";

const STEPS = ["you", "model", "owner"] as const;
type Step = (typeof STEPS)[number];

function NextButton({ last, ...rest }: React.ComponentProps<typeof Btn> & { last?: boolean }) {
  return (
    <Btn kind="primary" className="ml-auto px-4" {...rest}>
      {last ? t("setup.finish") : t("setup.continue")}
      <ArrowRight aria-hidden className="size-4" />
    </Btn>
  );
}

export function SetupPage() {
  const { me, space } = useSession();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>(me.setup.needs_name ? "you" : "model");
  const [name, setName] = useState(me.name);
  const [tz, setTz] = useState(space.time_zone && space.time_zone !== "UTC" ? space.time_zone : browserTimeZone());
  const [formOpen, setFormOpen] = useState(false);
  const [chosenId, setChosenId] = useState<string | null>(null);
  const [ownerModel, setOwnerModel] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const updateMe = useUpdateMe();
  const updateSpace = useUpdateSpace();
  const providers = useProviders();
  const presets = usePresets();
  const test = useTestProvider();
  const setRole = useSetRole();

  const list = providers.data ?? [];
  const provider: SchemaProviderView | undefined = list.find((p) => p.id === chosenId) ?? list[list.length - 1];
  const showForm = formOpen || (providers.isSuccess && list.length === 0);
  const ready = !!provider?.smoke.tools;
  const presetModel = presets.data?.find((p) => p.id === provider?.preset)?.default_model;
  const owner = ownerModel || provider?.smoke.model || presetModel || provider?.models?.[0]?.id || "";
  const idx = STEPS.indexOf(step);

  const saveYou = () => {
    if (!name.trim()) return setNameError(t("setup.you.needName"));
    setNameError(null);
    const jobs: Promise<unknown>[] = [];
    if (name.trim() !== me.name) jobs.push(updateMe.mutateAsync(name.trim()));
    if (tz.trim() && tz.trim() !== space.time_zone) jobs.push(updateSpace.mutateAsync({ time_zone: tz.trim() }));
    Promise.all(jobs).then(
      () => setStep("model"),
      (err) => toast.error(t("setup.saveFailed"), { description: errorText(err) }),
    );
  };

  const finish = () => {
    if (!provider || !owner) return;
    setRole.mutate(
      { role: "owner", targets: [targetFor(provider, owner)] },
      {
        onSuccess: async () => {
          await qc.refetchQueries({ queryKey: keys.me });
          void navigate({ to: "/", replace: true });
        },
        onError: (err) => toast.error(t("setup.saveFailed"), { description: errorText(err) }),
      },
    );
  };

  const back =
    idx > 0 ? (
      <button
        type="button"
        onClick={() => setStep(STEPS[idx - 1])}
        className="h-9 rounded-sm px-3 text-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
      >
        {t("setup.back")}
      </button>
    ) : null;

  let footer: React.ReactNode;
  if (step === "you") footer = <NextButton onClick={saveYou} busy={updateMe.isPending || updateSpace.isPending} />;
  else if (step === "model")
    footer = (
      <>
        {back}
        <NextButton onClick={() => setStep("owner")} disabled={!ready || showForm} />
      </>
    );
  else
    footer = (
      <>
        {back}
        <NextButton last onClick={finish} disabled={!owner} busy={setRole.isPending} />
      </>
    );

  return (
    <WizardFrame
      steps={STEPS.map((s) => ({ key: s, label: t(`setup.real.steps.${s}`) }))}
      step={step}
      title={t(`setup.real.${step}.title`)}
      body={t(`setup.real.${step}.body`)}
      footer={footer}
    >
      {step === "you" && (
        <form
          className="grid gap-4 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            saveYou();
          }}
        >
          <Field id="you-name" label={t("setup.you.name")} error={nameError}>
            <input
              id="you-name"
              value={name}
              maxLength={80}
              autoComplete="name"
              aria-invalid={!!nameError || undefined}
              onChange={(e) => setName(e.target.value)}
              className={inputCls}
            />
          </Field>
          <Field id="you-tz" label={t("setup.you.timeZone")} hint={t("setup.you.timeZoneHint")}>
            <TimeZoneInput id="you-tz" value={tz} onChange={setTz} describedBy="you-tz-hint" />
          </Field>
          <button type="submit" tabIndex={-1} aria-hidden className="sr-only">
            {t("setup.continue")}
          </button>
        </form>
      )}

      {step === "model" && (
        <div className="space-y-4">
          {showForm ? (
            <ProviderForm
              idPrefix="setup"
              submitLabel={t("setup.real.model.saveAndTest")}
              onSaved={(p) => {
                setChosenId(p.id);
                setFormOpen(false);
                test.mutate({ id: p.id });
              }}
              onCancel={list.length > 0 ? () => setFormOpen(false) : undefined}
            />
          ) : (
            provider && (
              <>
                <p className="text-sm text-ink-soft">
                  {t("setup.real.model.using", { name: provider.name })}
                  {provider.has_key && <span className="figures"> · •••• {provider.key_last4}</span>}
                </p>
                <ProviderTest provider={provider} idPrefix="setup-test" />
                {provider.tested_at && !ready && (
                  <p className="flex items-start gap-2 text-sm text-warn" role="alert">
                    <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0" />
                    {t("setup.real.model.needTools")}
                  </p>
                )}
                <button
                  type="button"
                  onClick={() => setFormOpen(true)}
                  className="text-sm text-ink-soft underline underline-offset-2 hover:text-ink"
                >
                  {t("setup.model.otherProvider")}
                </button>
              </>
            )
          )}
        </div>
      )}

      {step === "owner" && provider && (
        <div className="space-y-2">
          <label htmlFor="owner-model" className="block text-sm font-[var(--weight-medium)] text-ink">
            {t("setup.real.owner.label")}
          </label>
          <ModelPicker
            id="owner-model"
            models={provider.models ?? []}
            value={owner}
            onChange={setOwnerModel}
            describedBy="owner-model-hint"
          />
          <p id="owner-model-hint" className="text-xs text-ink-faint">
            {t("setup.real.owner.hint", { name: provider.name })}
          </p>
        </div>
      )}
    </WizardFrame>
  );
}
