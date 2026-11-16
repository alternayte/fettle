import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { CircleAlert, GraduationCap, History, MessageSquare, Monitor } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useArchiveBot,
  useAssignSkill,
  useDeleteBot,
  useDuplicateBot,
  useMakePrimary,
  useRestoreBot,
  useBot,
  useMoveBotZone,
  useZones,
  useBotMemory,
  useInstructionVersions,
  usePutBotMemory,
  useRestoreInstructions,
  useSession,
  useSkills,
  useUpdateBot,
} from "@/api/hooks";
import { Switch } from "@/components/ui/switch";
import type { SchemaBotBody, SchemaBotView } from "@/api/schema";
import { Notice, Page, Section } from "@/app/Page";
import { Btn, Field, inputCls, textareaCls } from "@/components/form";
import { BotMark } from "@/features/chat/BotMark";
import { ModelRoles } from "@/features/settings/ModelsPage";
import { NativeSelect } from "@/features/settings/ProviderForm";
import { TeachDialog } from "@/features/teach/TeachDialog";
import { t } from "@/lib/copy";
import { formatClock, formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";
import { MemoryEditor } from "./MemoryEditor";

function bodyOf(b: SchemaBotView): SchemaBotBody {
  return { name: b.name, job: b.job, public_desc: b.public_desc, avatar: b.avatar, instructions: b.instructions };
}

function useSave(bot: SchemaBotView) {
  const update = useUpdateBot(bot.id);
  const save = (patch: Partial<SchemaBotBody>, ok: string) =>
    update.mutate(
      { ...bodyOf(bot), ...patch },
      {
        onSuccess: () => toast.success(ok),
        onError: (err) => toast.error(t("botPage.saveFailed"), { description: errorText(err) }),
      },
    );
  return { save, saving: update.isPending };
}

function Profile({ bot }: { bot: SchemaBotView }) {
  const [name, setName] = useState(bot.name);
  const [job, setJob] = useState(bot.job);
  const [desc, setDesc] = useState(bot.public_desc);
  const { save, saving } = useSave(bot);
  const dirty = name !== bot.name || job !== bot.job || desc !== bot.public_desc;
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim()) return;
        save({ name: name.trim(), job: job.trim(), public_desc: desc.trim() }, t("botPage.saved"));
      }}
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="bot-name" label={t("botPage.name")}>
          <input
            id="bot-name"
            value={name}
            required
            maxLength={40}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field id="bot-job" label={t("botPage.job")} hint={t("botPage.jobHint")}>
          <input
            id="bot-job"
            value={job}
            maxLength={200}
            aria-describedby="bot-job-hint"
            onChange={(e) => setJob(e.target.value)}
            className={inputCls}
          />
        </Field>
      </div>
      <Field id="bot-desc" label={t("botPage.publicDesc")} hint={t("botPage.publicDescHint")}>
        <textarea
          id="bot-desc"
          value={desc}
          rows={2}
          maxLength={500}
          aria-describedby="bot-desc-hint"
          onChange={(e) => setDesc(e.target.value)}
          className={cn(textareaCls, "resize-y")}
        />
      </Field>
      <div className="flex justify-end">
        <Btn type="submit" kind="primary" size="sm" disabled={!dirty || !name.trim()} busy={saving}>
          {t("common.save")}
        </Btn>
      </div>
    </form>
  );
}

function Instructions({ bot }: { bot: SchemaBotView }) {
  const [text, setText] = useState(bot.instructions);
  const { save, saving } = useSave(bot);
  const dirty = text !== bot.instructions;
  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (dirty) save({ instructions: text }, t("botPage.instructionsSaved"));
      }}
    >
      <label htmlFor="bot-instructions" className="sr-only">
        {t("botPage.instructions")}
      </label>
      <textarea
        id="bot-instructions"
        value={text}
        rows={14}
        spellCheck={false}
        onChange={(e) => setText(e.target.value)}
        className={cn(textareaCls, "min-h-64 resize-y font-mono text-sm leading-6")}
      />
      <div className="flex items-center gap-2">
        <span className="figures text-ink-faint">{t("botPage.version", { rev: bot.instructions_rev })}</span>
        <div className="ml-auto flex items-center gap-1.5">
          {dirty && (
            <Btn kind="quiet" size="sm" onClick={() => setText(bot.instructions)}>
              {t("common.reset")}
            </Btn>
          )}
          <Btn type="submit" kind="primary" size="sm" disabled={!dirty} busy={saving}>
            {t("botPage.saveVersion")}
          </Btn>
        </div>
      </div>
    </form>
  );
}

function Versions({ botId }: { botId: string }) {
  const versions = useInstructionVersions(botId);
  const restore = useRestoreInstructions(botId);
  const [open, setOpen] = useState<number | null>(null);
  if (versions.isPending)
    return (
      <p className="text-sm text-ink-faint" role="status">
        {t("common.loading")}
      </p>
    );
  if (versions.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("botPage.versionsFailed")}>
        {errorText(versions.error)}
      </Notice>
    );
  if (versions.data.length === 0) return <p className="text-sm text-ink-faint">{t("botPage.noVersions")}</p>;
  return (
    <ol className="border-t border-rule">
      {versions.data.map((v) => (
        <li key={v.rev} className="border-b border-rule py-2">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="figures w-10 text-ink">{t("botPage.rev", { rev: v.rev })}</span>
            <span className="figures text-ink-faint">
              {formatDay(v.at)} {formatClock(v.at)}
            </span>
            <span className="text-xs text-ink-faint">
              {v.author_kind === "user"
                ? t("botPage.byPerson")
                : v.author_kind === "bot"
                  ? t("botPage.byBot")
                  : t("botPage.bySystem")}
            </span>
            {v.current && <span className="label-caps !text-ok">{t("botPage.current")}</span>}
            <div className="ml-auto flex items-center gap-1">
              <Btn
                kind="quiet"
                size="sm"
                aria-expanded={open === v.rev}
                onClick={() => setOpen((o) => (o === v.rev ? null : v.rev))}
              >
                {open === v.rev ? t("botPage.hide") : t("botPage.view")}
              </Btn>
              {!v.current && (
                <Btn
                  size="sm"
                  busy={restore.isPending && restore.variables === v.rev}
                  onClick={() =>
                    restore.mutate(v.rev, {
                      onSuccess: () => toast.success(t("botPage.restored", { rev: v.rev })),
                      onError: (err) => toast.error(t("botPage.restoreFailed"), { description: errorText(err) }),
                    })
                  }
                >
                  <History aria-hidden className="size-3.5" />
                  {t("botPage.restore")}
                </Btn>
              )}
            </div>
          </div>
          {open === v.rev && (
            <pre className="mt-2 max-h-72 overflow-auto rounded-xs border border-rule bg-surface-sunk p-2 font-mono text-xs whitespace-pre-wrap text-ink-soft">
              {v.instructions || t("botPage.emptyInstructions")}
            </pre>
          )}
        </li>
      ))}
    </ol>
  );
}

function BotSkills({ botId }: { botId: string }) {
  const { isOwner } = useSession();
  const skills = useSkills();
  const assign = useAssignSkill();
  const list = skills.data ?? [];
  if (skills.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("skills.loadFailed")}>
        {errorText(skills.error)}
      </Notice>
    );
  if (list.length === 0) return <p className="text-sm text-ink-faint">{t("botPage.noSkills")}</p>;
  return (
    <ul aria-label={t("botPage.skills")} className="divide-y divide-rule border-b border-rule">
      {list.map((s) => (
        <li key={s.id} className="grid grid-cols-[1fr_auto] items-center gap-x-3 py-2">
          <Link to="/skills/$skillId" params={{ skillId: s.id }} className="min-w-0">
            <span className="block truncate font-mono text-sm text-ink">{s.name}</span>
            <span className="block truncate text-xs text-ink-faint">{s.description}</span>
          </Link>
          <Switch
            checked={(s.bots ?? []).includes(botId)}
            disabled={!isOwner}
            aria-label={t("botPage.useSkill", { skill: s.name })}
            onCheckedChange={(on) =>
              assign.mutate({ skill: s.id, bot: botId, on }, { onError: (err) => toast.error(errorText(err)) })
            }
          />
        </li>
      ))}
    </ul>
  );
}

/** Manage holds duplicate, archive, primary and delete (§6.1, §9.8). */
function Computer({ bot }: { bot: SchemaBotView }) {
  const { isOwner } = useSession();
  const zones = useZones();
  const move = useMoveBotZone(bot.id);
  const [zoneId, setZoneId] = useState(bot.zone_id ?? "");
  const size = bot.zone_size ? t(`computers.sizes.${bot.zone_size}`).toLowerCase() : "";
  const target = (zones.data ?? []).find((z) => z.id === zoneId);
  return (
    <div className="space-y-4">
      <p className="text-sm text-ink-soft">
        {bot.zone_id ? t("botPage.worksIn", { bot: bot.name, zone: bot.zone_name, size }) : t("botPage.noZone")}
      </p>
      {isOwner && (zones.data ?? []).length > 0 && (
        <div>
          <div className="flex flex-wrap items-end gap-2">
            <Field id="bot-zone" label={t("botPage.moveTo")} className="w-64">
              <NativeSelect id="bot-zone" value={zoneId} onChange={setZoneId} describedBy="bot-zone-hint">
                {(zones.data ?? []).map((z) => (
                  <option key={z.id} value={z.id}>
                    {t("botsPage.zoneOption", { name: z.name, size: t(`computers.sizes.${z.size}`).toLowerCase() })}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Btn
              disabled={!zoneId || zoneId === bot.zone_id}
              busy={move.isPending}
              onClick={() =>
                move.mutate(zoneId, {
                  onSuccess: () => toast.success(t("botPage.moved", { bot: bot.name, zone: target?.name ?? "" })),
                  onError: (err) => toast.error(t("botPage.moveFailed"), { description: errorText(err) }),
                })
              }
            >
              {t("botPage.move")}
            </Btn>
          </div>
          <p id="bot-zone-hint" className="mt-1.5 text-xs text-ink-faint">
            {t("botPage.moveHint")}
          </p>
        </div>
      )}
      {bot.zone_size === "desktop" ? (
        <Link
          to="/screen/$botId"
          params={{ botId: bot.id }}
          className="inline-flex items-center gap-1.5 text-sm text-ink underline underline-offset-2"
        >
          <Monitor aria-hidden className="size-4" />
          {t("botPage.openScreen")}
        </Link>
      ) : (
        <p className="text-xs text-ink-faint">{t("botPage.noScreen")}</p>
      )}
      <TeachButton botId={bot.id} botName={bot.name} />
    </div>
  );
}

/** TeachButton starts teaching the bot a task on its screen (§13.3). */
function TeachButton({ botId, botName }: { botId: string; botName: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="space-y-1">
      <Btn size="sm" onClick={() => setOpen(true)}>
        <GraduationCap aria-hidden className="size-3.5" />
        {t("teach.action")}
      </Btn>
      <p className="text-xs text-ink-faint">{t("botPage.teachHint", { bot: botName })}</p>
      {open && <TeachDialog botId={botId} botName={botName} open={open} onOpenChange={setOpen} />}
    </div>
  );
}

function Manage({ bot }: { bot: SchemaBotView }) {
  const navigate = useNavigate();
  const dup = useDuplicateBot();
  const archive = useArchiveBot();
  const restore = useRestoreBot();
  const primary = useMakePrimary();
  const del = useDeleteBot();
  const [confirm, setConfirm] = useState("");
  const fail = (err: unknown) => toast.error(errorText(err));
  const archived = bot.status === "archived";
  return (
    <div className="divide-y divide-rule border-y border-rule">
      <div className="flex flex-wrap items-center gap-3 py-3">
        <p className="min-w-0 flex-1 text-sm text-ink-soft">{t("botPage.duplicateHint")}</p>
        <Btn
          size="sm"
          busy={dup.isPending}
          onClick={() =>
            dup.mutate(bot.id, {
              onSuccess: (b) => {
                toast.success(t("botPage.duplicated", { name: b.name }));
                void navigate({ to: "/bots/$botId", params: { botId: b.id } });
              },
              onError: fail,
            })
          }
        >
          {t("botPage.duplicate")}
        </Btn>
      </div>
      {!bot.is_primary && !archived && (
        <div className="flex flex-wrap items-center gap-3 py-3">
          <p className="min-w-0 flex-1 text-sm text-ink-soft">{t("botPage.primaryHint")}</p>
          <Btn
            size="sm"
            busy={primary.isPending}
            onClick={() => primary.mutate(bot.id, { onSuccess: () => toast(t("botPage.primaryAsked")), onError: fail })}
          >
            {t("botPage.makePrimary")}
          </Btn>
        </div>
      )}
      {bot.is_primary ? (
        <p className="py-3 text-sm text-ink-faint">{t("botPage.isPrimary")}</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3 py-3">
            <p className="min-w-0 flex-1 text-sm text-ink-soft">
              {archived ? t("botPage.isArchived") : t("botPage.archiveHint")}
            </p>
            {archived ? (
              <Btn
                size="sm"
                busy={restore.isPending}
                onClick={() =>
                  restore.mutate(bot.id, {
                    onSuccess: () => toast.success(t("botPage.restored", { name: bot.name })),
                    onError: fail,
                  })
                }
              >
                {t("botPage.restore")}
              </Btn>
            ) : (
              <Btn
                size="sm"
                busy={archive.isPending}
                onClick={() =>
                  archive.mutate(bot.id, {
                    onSuccess: () => toast.success(t("botPage.archived", { name: bot.name })),
                    onError: fail,
                  })
                }
              >
                {t("botPage.archive")}
              </Btn>
            )}
          </div>
          <form
            className="flex flex-wrap items-end gap-3 py-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (confirm !== bot.name) return;
              del.mutate(
                { bot: bot.id, confirm },
                {
                  onSuccess: () => {
                    toast.success(t("botPage.deleted", { name: bot.name }));
                    void navigate({ to: "/" });
                  },
                  onError: fail,
                },
              );
            }}
          >
            <div className="min-w-0 flex-1">
              <p className="text-sm text-ink-soft">{t("botPage.deleteHint", { name: bot.name })}</p>
              <label htmlFor="bot-delete" className="sr-only">
                {t("botPage.deleteLabel")}
              </label>
              <input
                id="bot-delete"
                value={confirm}
                autoComplete="off"
                placeholder={bot.name}
                onChange={(e) => setConfirm(e.target.value)}
                className={cn(inputCls, "mt-1.5 max-w-64")}
              />
            </div>
            <Btn type="submit" size="sm" kind="danger" disabled={confirm !== bot.name} busy={del.isPending}>
              {t("botPage.delete")}
            </Btn>
          </form>
        </>
      )}
    </div>
  );
}

export function BotPage() {
  const { botId } = useParams({ strict: false }) as { botId: string };
  const bot = useBot(botId);
  const memory = useBotMemory(botId);
  const putMemory = usePutBotMemory(botId);
  const { isOwner } = useSession();

  useEffect(() => {
    if (bot.isSuccess && window.location.hash === "#memory") {
      document.getElementById("memory")?.scrollIntoView({ block: "start" });
    }
  }, [bot.isSuccess]);

  if (bot.isPending)
    return (
      <Page title={t("botPage.title")}>
        <p className="text-sm text-ink-faint" role="status">
          {t("common.loading")}
        </p>
      </Page>
    );
  if (bot.isError)
    return (
      <Page title={t("botPage.title")}>
        <Notice icon={CircleAlert} tone="danger" title={t("botPage.loadFailed")}>
          {errorText(bot.error)}
        </Notice>
      </Page>
    );
  const b = bot.data;
  return (
    <Page
      title={b.name}
      subtitle={t("botPage.title")}
      actions={
        <Link
          to="/chat/$chatId"
          params={{ chatId: b.chat_id }}
          className="inline-flex h-8 items-center gap-1.5 rounded-sm px-2 text-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
        >
          <MessageSquare aria-hidden className="size-4" />
          <span className="hidden sm:inline">{t("botPage.openChat")}</span>
          <span className="sr-only sm:hidden">{t("botPage.openChat")}</span>
        </Link>
      }
    >
      <div className="mb-6 flex items-center gap-3">
        <BotMark name={b.name} size="lg" />
        <div className="min-w-0">
          <p className="serif-title truncate text-2xl text-ink">{b.name}</p>
          <p className="truncate text-sm text-ink-faint">
            {b.is_primary ? `${t("bots.primary")} · ` : ""}
            {b.job}
          </p>
        </div>
      </div>
      <Section id="profile" title={t("botPage.profile")}>
        <Profile key={`${b.name}|${b.job}|${b.public_desc}`} bot={b} />
      </Section>
      <Section id="computer" title={t("botPage.computer")}>
        <Computer key={b.zone_id ?? ""} bot={b} />
      </Section>
      <Section id="instructions" title={t("botPage.instructions")}>
        <Instructions key={b.instructions_rev} bot={b} />
      </Section>
      <Section id="versions" title={t("botPage.versions")}>
        <Versions botId={b.id} />
      </Section>
      {isOwner && (
        <Section id="models" title={t("botPage.models")}>
          <p className="mb-2 text-sm text-ink-soft">{t("botPage.modelsBody")}</p>
          <ModelRoles bot={b.id} />
        </Section>
      )}
      <Section id="skills" title={t("botPage.skills")}>
        <p className="mb-2 text-sm text-ink-soft">{t("botPage.skillsBody")}</p>
        <BotSkills botId={b.id} />
      </Section>
      <Section id="memory" title={t("botPage.memory")}>
        <p className="mb-2 text-sm text-ink-soft">{t("botPage.memoryBody")}</p>
        <MemoryEditor
          id="bot-memory"
          label={t("botPage.memory")}
          query={memory}
          onSave={(c) => putMemory.mutateAsync(c)}
          saving={putMemory.isPending}
        />
      </Section>
      <Section id="manage" title={t("botPage.manage")}>
        <Manage bot={b} />
      </Section>
    </Page>
  );
}
