import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { CircleAlert, Download, FileUp, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useAssignSkill,
  useBots,
  useDeleteSkill,
  useDraftSkill,
  useImportSkill,
  useSaveSkill,
  useSession,
  useSetSetting,
  useSettings,
  useSkill,
  useSkills,
  useSyncSkael,
  useVault,
} from "@/api/hooks";
import type { SchemaSkillVersionView, SchemaSkillView } from "@/api/schema";
import { Notice, Page, Section } from "@/app/Page";
import { Btn, Field, inputCls, textareaCls } from "@/components/form";
import { Switch } from "@/components/ui/switch";
import { NativeSelect } from "@/features/settings/ProviderForm";
import { t, tn } from "@/lib/copy";
import { formatClock, formatDay } from "@/lib/format";
import { cn } from "@/lib/utils";

const TEMPLATE =
  "---\nname: new-skill\ndescription: Say when a bot uses this skill.\n---\n\n# New skill\n\n## Steps\n\n1. \n";

/** SkillEditor edits SKILL.md text. Saving makes a new version of the skill with that name. */
function SkillEditor({
  initial,
  onSaved,
  onCancel,
  label,
}: {
  initial: string;
  onSaved: (s: SchemaSkillView) => void;
  onCancel: () => void;
  label: string;
}) {
  const save = useSaveSkill();
  const [text, setText] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      noValidate
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        setError(null);
        save.mutate(text, { onSuccess: onSaved, onError: (err) => setError(errorText(err)) });
      }}
    >
      <Field id="skill-md" label={label} hint={t("skills.mdHint")}>
        <textarea
          id="skill-md"
          rows={18}
          value={text}
          spellCheck={false}
          aria-describedby="skill-md-hint"
          onChange={(e) => setText(e.target.value)}
          className={cn(textareaCls, "font-mono text-sm leading-relaxed")}
        />
      </Field>
      {error && (
        <p role="alert" className="rounded-sm border border-danger/40 bg-danger-wash px-3 py-2 text-sm text-ink">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-1.5">
        <Btn kind="quiet" onClick={onCancel}>
          {t("common.cancel")}
        </Btn>
        <Btn type="submit" kind="primary" busy={save.isPending}>
          {t("skills.save")}
        </Btn>
      </div>
    </form>
  );
}

function SkaelSync() {
  const settings = useSettings();
  const set = useSetSetting();
  const vault = useVault();
  const sync = useSyncSkael();
  const value = (k: string) => String((settings.data ?? []).find((s) => s.key === k)?.value ?? "");
  const [url, setUrl] = useState<string | null>(null);
  const [setName, setSetName] = useState<string | null>(null);
  const [key, setKey] = useState<string | null>(null);
  const secrets = (vault.data ?? []).filter((v) => v.kind === "secret");
  const cur = {
    url: url ?? value("skael.url"),
    set: setName ?? value("skael.set"),
    key: key ?? value("skael.key_item"),
  };
  const dirty = cur.url !== value("skael.url") || cur.set !== value("skael.set") || cur.key !== value("skael.key_item");
  const configured = !!(value("skael.url") && value("skael.set") && value("skael.key_item"));
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        void Promise.all([
          set.mutateAsync({ key: "skael.url", value: cur.url.trim() }),
          set.mutateAsync({ key: "skael.set", value: cur.set.trim() }),
          set.mutateAsync({ key: "skael.key_item", value: cur.key }),
        ])
          .then(() => {
            setUrl(null);
            setSetName(null);
            setKey(null);
            toast.success(t("skills.skael.saved"));
          })
          .catch((err) => toast.error(errorText(err)));
      }}
    >
      <p className="max-w-[var(--measure)] text-sm text-ink-soft">{t("skills.skael.intro")}</p>
      <div className="grid gap-4 sm:grid-cols-3">
        <Field id="skael-url" label={t("skills.skael.url")}>
          <input
            id="skael-url"
            type="url"
            value={cur.url}
            placeholder="https://skael.acme.test"
            onChange={(e) => setUrl(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field id="skael-set" label={t("skills.skael.set")}>
          <input id="skael-set" value={cur.set} onChange={(e) => setSetName(e.target.value)} className={inputCls} />
        </Field>
        <Field id="skael-key" label={t("skills.skael.key")}>
          <NativeSelect id="skael-key" value={cur.key} onChange={setKey}>
            <option value="">{t("skills.skael.noKey")}</option>
            {secrets.map((s) => (
              <option key={s.id} value={s.name}>
                {s.name}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
      {secrets.length === 0 && (
        <p className="text-xs text-ink-faint">
          {t("skills.skael.keyHint")}{" "}
          <Link to="/settings/vault" className="text-ink underline underline-offset-2">
            {t("skills.skael.vault")}
          </Link>
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-1.5">
        {dirty && (
          <Btn type="submit" kind="primary" busy={set.isPending}>
            {t("common.save")}
          </Btn>
        )}
        <Btn
          disabled={!configured || dirty}
          busy={sync.isPending}
          onClick={() =>
            sync.mutate(undefined, {
              onSuccess: (r) => toast.success(t("skills.skael.synced", { skills: r.skills, updated: r.updated })),
              onError: (err) => toast.error(t("skills.skael.failed"), { description: errorText(err) }),
            })
          }
        >
          <RefreshCw aria-hidden className="size-3.5" />
          {t("skills.skael.syncNow")}
        </Btn>
      </div>
    </form>
  );
}

function ImportButton() {
  const ref = useRef<HTMLInputElement>(null);
  const imp = useImportSkill();
  const navigate = useNavigate();
  return (
    <>
      <input
        ref={ref}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        tabIndex={-1}
        aria-hidden
        aria-label={t("skills.importFile")}
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (!f) return;
          imp.mutate(f, {
            onSuccess: (sk) => {
              toast.success(t("skills.imported", { name: sk.name, version: sk.current_version }));
              void navigate({ to: "/skills/$skillId", params: { skillId: sk.id } });
            },
            onError: (err) => toast.error(t("skills.importFailed"), { description: errorText(err) }),
          });
        }}
      />
      <Btn size="sm" busy={imp.isPending} onClick={() => ref.current?.click()}>
        <FileUp aria-hidden className="size-3.5" />
        {t("skills.import")}
      </Btn>
    </>
  );
}

/** DraftFromRun loads the Save as skill draft of a run into the editor for review. */
function DraftFromRun({ run, onDone }: { run: string; onDone: () => void }) {
  const draft = useDraftSkill();
  const navigate = useNavigate();
  const { mutate } = draft;
  useEffect(() => mutate(run), [mutate, run]);
  if (draft.isError)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("skills.draftFailed")}>
        {errorText(draft.error)}
      </Notice>
    );
  if (!draft.data)
    return (
      <p role="status" className="text-sm text-ink-faint">
        {t("skills.drafting")}
      </p>
    );
  return (
    <div className="rounded-sm border border-glaze/50 bg-surface p-4">
      <p className="mb-3 max-w-[var(--measure)] text-sm text-ink-soft">{t("skills.draftIntro")}</p>
      <SkillEditor
        label={t("skills.draftLabel")}
        initial={draft.data}
        onCancel={onDone}
        onSaved={(sk) => {
          toast.success(t("skills.saved", { name: sk.name, version: sk.current_version }));
          void navigate({ to: "/skills/$skillId", params: { skillId: sk.id } });
        }}
      />
    </div>
  );
}

export function SkillsPage() {
  const { isOwner } = useSession();
  const skills = useSkills();
  const search = useSearch({ strict: false }) as { from_run?: string };
  const navigate = useNavigate();
  const [adding, setAdding] = useState(false);
  const list = skills.data ?? [];
  const closeDraft = () => void navigate({ to: "/skills", search: {} });
  return (
    <Page title={t("skills.title")} wide>
      <p className="mb-6 max-w-[var(--measure)] text-sm text-ink-soft">{t("skills.intro")}</p>
      {search.from_run && isOwner && (
        <div className="mb-8">
          <DraftFromRun run={search.from_run} onDone={closeDraft} />
        </div>
      )}
      <Section
        id="library"
        title={t("skills.library")}
        aside={
          isOwner &&
          !adding && (
            <>
              <ImportButton />
              <Btn size="sm" onClick={() => setAdding(true)}>
                <Plus aria-hidden className="size-3.5" />
                {t("skills.new")}
              </Btn>
            </>
          )
        }
      >
        {adding && (
          <div className="mb-3 rounded-sm border border-rule bg-surface p-4">
            <SkillEditor
              label="SKILL.md"
              initial={TEMPLATE}
              onCancel={() => setAdding(false)}
              onSaved={(sk) => {
                setAdding(false);
                toast.success(t("skills.saved", { name: sk.name, version: sk.current_version }));
              }}
            />
          </div>
        )}
        {skills.isError ? (
          <Notice icon={CircleAlert} tone="danger" title={t("skills.loadFailed")}>
            {errorText(skills.error)}
          </Notice>
        ) : list.length === 0 && !adding ? (
          <p className="border-y border-rule py-3 text-sm text-ink-faint">{t("skills.none")}</p>
        ) : (
          <ul aria-label={t("skills.library")} className="divide-y divide-rule border-b border-rule">
            {list.map((s) => (
              <li key={s.id}>
                <Link
                  to="/skills/$skillId"
                  params={{ skillId: s.id }}
                  className="grid grid-cols-[1fr_auto] items-baseline gap-x-4 gap-y-0.5 py-2.5 hover:bg-surface-sunk sm:grid-cols-[12rem_1fr_auto]"
                >
                  <span className="truncate font-mono text-sm text-ink">{s.name}</span>
                  <span className="figures text-right text-xs text-ink-faint sm:order-last">
                    {s.source === "skael" ? "Skael · " : ""}v{s.current_version} ·{" "}
                    {tn("skills.botCount", (s.bots ?? []).length)}
                  </span>
                  <span className="col-span-2 truncate text-sm text-ink-soft sm:col-span-1">{s.description}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
        {!isOwner && <p className="mt-2 text-xs text-ink-faint">{t("skills.ownerOnly")}</p>}
      </Section>
      {isOwner && (
        <Section id="skael" title={t("skills.skael.title")}>
          <SkaelSync />
        </Section>
      )}
    </Page>
  );
}

function VersionRow({
  v,
  current,
  open,
  onToggle,
}: {
  v: SchemaSkillVersionView;
  current: boolean;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <li className="py-2">
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className="grid w-full grid-cols-[3.5rem_1fr_auto] items-baseline gap-3 text-left text-sm"
      >
        <span className="figures text-ink">v{v.version}</span>
        <span className="figures truncate text-ink-soft">
          {formatDay(v.created_at)} {formatClock(v.created_at)}
          {v.upstream ? ` · Skael v${v.upstream}` : ""}
          {v.has_evals ? ` · ${t("skills.evals")}` : ""}
        </span>
        <span className="figures text-xs text-ink-faint">{current ? t("skills.current") : v.sha256.slice(0, 10)}</span>
      </button>
      {open && (
        <pre className="mt-2 ml-[4.25rem] max-h-80 overflow-auto rounded-sm border border-rule bg-surface-sunk p-3 font-mono text-xs whitespace-pre-wrap text-ink-soft">
          {v.skill_md}
        </pre>
      )}
    </li>
  );
}

export function SkillPage() {
  const { skillId } = useParams({ strict: false }) as { skillId: string };
  const { isOwner, space } = useSession();
  const q = useSkill(skillId);
  const bots = useBots();
  const assign = useAssignSkill();
  const del = useDeleteSkill();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [openV, setOpenV] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  if (q.isPending)
    return (
      <Page title={t("skills.title")}>
        <p role="status" className="text-sm text-ink-faint">
          {t("common.loading")}
        </p>
      </Page>
    );
  if (q.isError)
    return (
      <Page title={t("skills.title")}>
        <Notice icon={CircleAlert} tone="danger" title={t("skills.loadFailed")}>
          {errorText(q.error)}
        </Notice>
      </Page>
    );
  const sk = q.data.skill;
  const versions = q.data.versions ?? [];
  const cur = versions.find((v) => v.version === sk.current_version);
  const files = cur?.files ?? [];
  const has = new Set(sk.bots ?? []);
  const skael = sk.source === "skael";
  return (
    <Page
      title={sk.name}
      subtitle={t("skills.title")}
      actions={
        <a
          href={`/api/v1/spaces/${space.id}/skills/${sk.id}/export`}
          download={`${sk.name}.zip`}
          className="inline-flex h-8 items-center gap-1.5 rounded-sm px-2 text-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
        >
          <Download aria-hidden className="size-4" />
          <span>{t("skills.export")}</span>
        </a>
      }
    >
      <div className="mb-6">
        <p className="font-mono text-2xl text-ink">{sk.name}</p>
        <p className="mt-1 max-w-[var(--measure)] text-sm text-ink-soft">{sk.description}</p>
        <p className="figures mt-1 text-xs text-ink-faint">
          v{sk.current_version} · {skael ? t("skills.fromSkael") : t("skills.local")}
        </p>
      </div>
      <Section
        id="skill-text"
        title="SKILL.md"
        aside={
          isOwner &&
          !editing &&
          !skael && (
            <Btn size="sm" kind="quiet" onClick={() => setEditing(true)}>
              {t("common.edit")}
            </Btn>
          )
        }
      >
        {editing && cur ? (
          <SkillEditor
            label="SKILL.md"
            initial={cur.skill_md}
            onCancel={() => setEditing(false)}
            onSaved={(s) => {
              setEditing(false);
              toast.success(t("skills.saved", { name: s.name, version: s.current_version }));
            }}
          />
        ) : (
          <pre className="max-h-[28rem] overflow-auto rounded-sm border border-rule bg-surface-sunk p-3 font-mono text-sm whitespace-pre-wrap text-ink">
            {cur?.skill_md}
          </pre>
        )}
        {skael && <p className="mt-2 text-xs text-ink-faint">{t("skills.skaelOwned")}</p>}
      </Section>
      {files.length > 1 && (
        <Section id="files" title={t("skills.files")}>
          <ul className="space-y-0.5 font-mono text-sm text-ink-soft">
            {files.map((f) => (
              <li key={f}>{f}</li>
            ))}
          </ul>
          <p className="mt-2 text-xs text-ink-faint">{t("skills.filesHint", { dir: `.skills/${sk.name}/` })}</p>
        </Section>
      )}
      <Section id="bots" title={t("skills.bots")}>
        <ul aria-label={t("skills.bots")} className="divide-y divide-rule border-b border-rule">
          {(bots.data ?? []).map((b) => (
            <li key={b.id} className="flex items-center gap-3 py-2">
              <span className="min-w-0 flex-1 truncate text-sm text-ink">{b.name}</span>
              <Switch
                checked={has.has(b.id)}
                disabled={!isOwner}
                aria-label={t("skills.give", { bot: b.name })}
                onCheckedChange={(on) =>
                  assign.mutate({ skill: sk.id, bot: b.id, on }, { onError: (err) => toast.error(errorText(err)) })
                }
              />
            </li>
          ))}
        </ul>
      </Section>
      <Section id="versions" title={t("skills.versions")}>
        <ol aria-label={t("skills.versions")} className="divide-y divide-rule">
          {versions.map((v) => (
            <VersionRow
              key={v.version}
              v={v}
              current={v.version === sk.current_version}
              open={openV === v.version}
              onToggle={() => setOpenV((o) => (o === v.version ? null : v.version))}
            />
          ))}
        </ol>
      </Section>
      {isOwner && (
        <div className="mt-10 flex justify-end">
          <Btn
            size="sm"
            kind="danger"
            busy={del.isPending}
            onClick={() => {
              if (!confirmDelete) return setConfirmDelete(true);
              del.mutate(sk.id, {
                onSuccess: () => {
                  toast.success(t("skills.deleted", { name: sk.name }));
                  void navigate({ to: "/skills" });
                },
                onError: (err) => toast.error(errorText(err)),
              });
            }}
          >
            <Trash2 aria-hidden className="size-3.5" />
            {confirmDelete ? t("skills.confirmDelete") : t("skills.delete")}
          </Btn>
        </div>
      )}
    </Page>
  );
}
