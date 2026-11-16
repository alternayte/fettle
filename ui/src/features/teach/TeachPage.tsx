import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { CircleAlert, GraduationCap, Loader, Mic, Square, X } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import {
  useBot,
  useControlScreen,
  useStartTeach,
  useTeach,
  useTeachDiscard,
  useTeachDone,
  useTeachNote,
  useTeachVoiceNote,
} from "@/api/hooks";
import type { SchemaTeachStepView, SchemaTeachView } from "@/api/schema";
import { Btn, textareaCls } from "@/components/form";
import { Stream, type Mode } from "@/features/screen/Stream";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";
import { StepIcon } from "./StepIcon";
import { stepText } from "./steps";
import { TeachReview } from "./TeachReview";
import { TeachTerminal } from "./TeachTerminal";

/** StepList shows the recorded steps, newest last, with their notes. */
function StepList({ steps }: { steps: SchemaTeachStepView[] }) {
  const end = useRef<HTMLLIElement>(null);
  useEffect(() => {
    // Newer browsers return a promise here; an effect must not return it.
    void end.current?.scrollIntoView({ block: "nearest" });
  }, [steps.length]);
  if (steps.length === 0) return <p className="px-3 py-4 text-sm text-ink-faint">{t("teach.noSteps")}</p>;
  return (
    <ol aria-label={t("teach.steps")} className="space-y-px">
      {steps.map((s, i) => (
        <li key={s.seq} ref={i === steps.length - 1 ? end : undefined} className="px-3 py-2 text-sm">
          <div className="flex items-start gap-2">
            <span className="figures w-5 shrink-0 text-right text-xs text-ink-faint">{i + 1}</span>
            <StepIcon s={s} />
            <div className="min-w-0 flex-1">
              <p className="label-caps text-ink-faint">{t(`teach.kind.${s.kind === "browser" ? s.action : s.kind}`)}</p>
              <p className={cn("break-words text-ink", s.kind === "command" && "font-mono text-xs")}>{stepText(s)}</p>
              {s.kind === "command" && (
                <p className="text-xs text-ink-faint">
                  {t("teach.step.exit", {
                    cwd: !s.cwd || s.cwd === "." ? t("teach.step.home") : s.cwd,
                    code: s.exit_code,
                  })}
                </p>
              )}
              {(s.kind === "note" ? (s.notes ?? []).slice(1) : (s.notes ?? [])).map((n, j) => (
                <p key={j} className="mt-1 border-l-2 border-glaze/50 pl-2 text-xs text-ink-soft">
                  {n}
                </p>
              ))}
            </div>
          </div>
        </li>
      ))}
    </ol>
  );
}

/** NoteBox adds a typed or spoken note to the current step. */
function NoteBox({ session }: { session: SchemaTeachView }) {
  const note = useTeachNote(session.id);
  const voice = useTeachVoiceNote(session.id);
  const [text, setText] = useState("");
  const [recording, setRecording] = useState(false);
  const rec = useRef<MediaRecorder | null>(null);
  const send = (newStep: boolean) => {
    const v = text.trim();
    if (!v) return;
    note.mutate(
      { text: v, new_step: newStep || undefined },
      {
        onSuccess: () => setText(""),
        onError: (err) => toast.error(t("teach.noteFailed"), { description: errorText(err) }),
      },
    );
  };
  const startVoice = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mr = new MediaRecorder(stream);
      const parts: Blob[] = [];
      mr.ondataavailable = (e) => parts.push(e.data);
      mr.onstop = () => {
        stream.getTracks().forEach((tr) => tr.stop());
        const blob = new Blob(parts, { type: mr.mimeType || "audio/webm" });
        voice.mutate(blob, { onError: (err) => toast.error(t("teach.voiceFailed"), { description: errorText(err) }) });
      };
      mr.start();
      rec.current = mr;
      setRecording(true);
    } catch (err) {
      toast.error(t("teach.micFailed"), { description: errorText(err) });
    }
  };
  const stopVoice = () => {
    rec.current?.stop();
    rec.current = null;
    setRecording(false);
  };
  return (
    <form
      className="space-y-2 border-t border-rule p-3"
      onSubmit={(e) => {
        e.preventDefault();
        send(false);
      }}
    >
      <label htmlFor="teach-note" className="block text-xs text-ink-soft">
        {t("teach.noteLabel")}
      </label>
      <textarea
        id="teach-note"
        rows={2}
        maxLength={2000}
        value={text}
        placeholder={t("teach.notePlaceholder")}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            send(false);
          }
        }}
        className={cn(textareaCls, "text-sm")}
      />
      <div className="flex flex-wrap items-center gap-1.5">
        <Btn size="sm" type="submit" busy={note.isPending} disabled={!text.trim()}>
          {t("teach.addNote")}
        </Btn>
        <Btn size="sm" kind="quiet" disabled={!text.trim() || note.isPending} onClick={() => send(true)}>
          {t("teach.addNoteStep")}
        </Btn>
        {session.transcribe && (
          <Btn
            size="sm"
            kind={recording ? "act" : "plain"}
            className="ml-auto"
            busy={voice.isPending}
            aria-pressed={recording}
            aria-label={recording ? t("teach.voiceStop") : t("teach.voiceStart")}
            title={recording ? t("teach.voiceStop") : t("teach.voiceStart")}
            onClick={recording ? stopVoice : () => void startVoice()}
          >
            {recording ? <Square aria-hidden className="size-3.5" /> : <Mic aria-hidden className="size-3.5" />}
            {recording ? t("teach.voiceStop") : t("teach.voice")}
          </Btn>
        )}
      </div>
    </form>
  );
}

/** Recorder is the teach view while the person does the task: the screen, the terminal and the steps. */
function Recorder({ session, botName }: { session: SchemaTeachView; botName: string }) {
  const control = useControlScreen();
  const resume = useStartTeach();
  const done = useTeachDone(session.id);
  const discard = useTeachDiscard(session.id);
  const [mode, setMode] = useState<Mode | null>(null);
  const [lost, setLost] = useState(false);
  // Opening the view resumes the session: it takes the control lease and starts the
  // recorder again when the computer started again.
  const take = useCallback(() => {
    setLost(false);
    resume.mutate(
      { bot_id: session.bot_id },
      {
        onSuccess: (r) => setMode({ token: r.control.token, control: true }),
        onError: (err) => toast.error(t("teach.controlFailed"), { description: errorText(err) }),
      },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.bot_id]);
  useEffect(take, [take]);
  // A teach session holds the 15-minute control lease and renews it.
  useEffect(() => {
    const id = window.setInterval(() => control.mutate(session.bot_id), 10 * 60 * 1000);
    return () => window.clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.bot_id]);
  const onLost = useCallback(() => setLost(true), []);
  return (
    <>
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-rule-strong px-3 py-2 sm:px-4">
        <GraduationCap aria-hidden className="size-4 shrink-0 text-glaze" />
        <h1 className="serif-title min-w-0 truncate text-lg text-ink">{t("teach.title", { bot: botName })}</h1>
        <span
          role="status"
          className="label-caps flex items-center gap-1.5 rounded-sm border border-danger/50 px-1.5 py-0.5 text-danger"
        >
          <span aria-hidden className="size-1.5 animate-pulse rounded-full bg-danger" />
          {t("teach.recording")}
        </span>
        <span className="hidden truncate text-sm text-ink-soft md:inline">{session.title}</span>
        <div className="flex w-full items-center gap-2 sm:ml-auto sm:w-auto">
          <Btn
            size="sm"
            kind="quiet"
            busy={discard.isPending}
            onClick={() => discard.mutate(undefined, { onError: (err) => toast.error(errorText(err)) })}
          >
            {t("teach.discard")}
          </Btn>
          <Btn
            size="sm"
            kind="primary"
            busy={done.isPending}
            title={t("teach.doneHint")}
            onClick={() =>
              done.mutate(undefined, {
                onError: (err) => toast.error(t("teach.doneFailed"), { description: errorText(err) }),
              })
            }
          >
            {t("teach.done")}
          </Btn>
        </div>
      </header>
      <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)_auto] lg:grid-cols-[minmax(0,1fr)_21rem] lg:grid-rows-1">
        <div className="grid min-h-0 grid-rows-[minmax(0,3fr)_minmax(10rem,2fr)]">
          <section aria-label={t("teach.screen")} className="relative min-h-0 bg-surface-sunk">
            {mode && !lost && <Stream key={mode.token} mode={mode} onLost={onLost} />}
            {!mode && !resume.isError && (
              <p role="status" className="absolute inset-0 flex items-center justify-center text-sm text-ink-faint">
                {t("screen.connecting")}
              </p>
            )}
            {(lost || resume.isError) && (
              <div className="absolute inset-0 flex items-center justify-center p-4">
                <div
                  role="alert"
                  className="flex max-w-md items-start gap-2.5 rounded-sm border border-danger/40 bg-surface px-4 py-3 text-sm"
                >
                  <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-danger" />
                  <div>
                    <p className="font-[var(--weight-medium)]">
                      {resume.isError ? errorText(resume.error) : t("screen.lost")}
                    </p>
                    <Btn size="sm" className="mt-3" onClick={take}>
                      {t("screen.reconnect")}
                    </Btn>
                  </div>
                </div>
              </div>
            )}
          </section>
          <section aria-label={t("teach.terminal")} className="min-h-0 border-t border-rule-strong">
            {mode && <TeachTerminal token={mode.token} session={session.id} />}
          </section>
        </div>
        <aside className="flex max-h-[40vh] min-h-0 flex-col border-t border-rule-strong bg-surface lg:max-h-none lg:border-t-0 lg:border-l">
          <h2 className="label-caps border-b border-rule px-3 py-2 text-ink-faint">{t("teach.steps")}</h2>
          <div className="min-h-0 flex-1 overflow-y-auto">
            <StepList steps={(session.steps ?? []).filter((s) => !s.removed)} />
          </div>
          <NoteBox session={session} />
        </aside>
      </div>
    </>
  );
}

/** TeachPage is the teach view of a session (§13.3): recording, then the questions, then the review. */
export function TeachPage() {
  const { teachId } = useParams({ strict: false }) as { teachId: string };
  const navigate = useNavigate();
  const q = useTeach(teachId);
  const s = q.data;
  const bot = useBot(s?.bot_id ?? "");
  const name = bot.data?.name ?? "";
  const close = () => {
    if (window.history.length > 1) window.history.back();
    else void navigate({ to: "/" });
  };
  const recording = s?.status === "recording" && s.mine;
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t("teach.title", { bot: name })}
      className="fixed inset-0 z-50 flex flex-col bg-ground"
    >
      {!recording && (
        <header className="flex items-center gap-3 border-b border-rule-strong px-3 py-2 sm:px-4">
          <Btn size="sm" kind="quiet" onClick={close} aria-label={t("teach.close")}>
            <X aria-hidden className="size-4" />
          </Btn>
          <GraduationCap aria-hidden className="size-4 shrink-0 text-glaze" />
          <h1 className="serif-title min-w-0 truncate text-lg text-ink">{s ? s.title : t("teach.loading")}</h1>
          {s && (
            <span className="label-caps rounded-sm border border-rule px-1.5 py-0.5 text-ink-faint">
              {t(`teach.status.${s.status}`)}
            </span>
          )}
        </header>
      )}
      {q.isPending && (
        <p role="status" className="flex flex-1 items-center justify-center text-sm text-ink-faint">
          <Loader aria-hidden className="mr-2 size-4 animate-spin" />
          {t("teach.loading")}
        </p>
      )}
      {q.isError && (
        <p role="alert" className="p-6 text-sm text-danger">
          {errorText(q.error)}
        </p>
      )}
      {s && recording && <Recorder session={s} botName={name} />}
      {s && s.status === "recording" && !recording && (
        <p className="p-6 text-sm text-ink-soft">{t("teach.otherRecording")}</p>
      )}
      {s && (s.status === "asking" || s.status === "drafting") && (
        <div className="mx-auto w-full max-w-2xl space-y-4 overflow-y-auto p-6">
          <p className="flex items-center gap-2 text-sm text-ink">
            <Loader aria-hidden className="size-4 animate-spin text-glaze" />
            {s.status === "asking"
              ? (s.questions ?? []).length === 0
                ? t("teach.preparing", { bot: name })
                : t("teach.asking", { bot: name })
              : t("teach.drafting", { bot: name })}
          </p>
          {(s.questions ?? []).length > 0 && (
            <ol className="space-y-2">
              {(s.questions ?? []).map((qq) => (
                <li key={qq.card_id} className="rounded-sm border border-rule bg-surface px-3 py-2 text-sm">
                  <p className="text-ink">{qq.text}</p>
                  <p className={cn("mt-0.5 text-xs", qq.answer ? "text-ink-soft" : "text-ink-faint")}>
                    {qq.answer ? qq.answer : t("teach.notAnswered")}
                  </p>
                </li>
              ))}
            </ol>
          )}
          {s.status === "asking" && (s.questions ?? []).length > 0 && (
            <Link
              to="/chat/$chatId"
              params={{ chatId: s.chat_id }}
              search={{ card: (s.questions ?? []).find((x) => !x.answer)?.card_id }}
              className="inline-flex h-8 items-center rounded-sm bg-glaze px-3 text-sm font-[var(--weight-medium)] text-glaze-ink hover:brightness-105"
            >
              {t("teach.answerInChat")}
            </Link>
          )}
        </div>
      )}
      {s && (s.status === "review" || s.status === "saved" || s.status === "failed" || s.status === "discarded") && (
        <TeachReview session={s} />
      )}
    </div>
  );
}
