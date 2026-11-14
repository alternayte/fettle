import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import { answerCard, fetchMessages, keys, LIVE_STATES, postMessage, type CardAction } from "@/api/hooks";
import type { SchemaMessagePage, SchemaMessageView, SchemaRunLine } from "@/api/schema";
import { useEventStream } from "@/api/sse";
import { t } from "@/lib/copy";
import type { CardAnswer } from "./CardView";
import { toMessage } from "./map";
import type { Message } from "./types";

interface State {
  list: SchemaMessageView[];
  runs: Record<string, SchemaRunLine>;
  /** Cursor of the next older page; empty when the oldest message is loaded. */
  next: string;
  loaded: boolean;
  loadingOlder: boolean;
  error: string | null;
}

type Action =
  | { type: "page"; page: SchemaMessagePage; older: boolean }
  | { type: "olderStart" }
  | { type: "olderFail" }
  | { type: "error"; error: string }
  | { type: "upsert"; m: SchemaMessageView }
  | { type: "deltas"; d: Map<string, string> }
  | { type: "remove"; id: string }
  | { type: "run"; line: SchemaRunLine };

const initial: State = { list: [], runs: {}, next: "", loaded: false, loadingOlder: false, error: null };

const time = (m: SchemaMessageView) => Date.parse(m.created_at) || 0;

function merge(existing: SchemaMessageView | undefined, incoming: SchemaMessageView): SchemaMessageView {
  if (!existing) return incoming;
  // A late "reply started" event must not wipe text that already arrived.
  if (incoming.streaming && !existing.streaming) return existing;
  if (incoming.streaming && existing.streaming) return { ...incoming, body: existing.body || incoming.body };
  return incoming;
}

function upsertAll(list: SchemaMessageView[], incoming: SchemaMessageView[]): SchemaMessageView[] {
  const byId = new Map(list.map((m) => [m.id, m]));
  let changed = false;
  for (const m of incoming) {
    const next = merge(byId.get(m.id), m);
    if (next !== byId.get(m.id)) {
      byId.set(m.id, next);
      changed = true;
    }
  }
  if (!changed) return list;
  return [...byId.values()].sort((a, b) => time(a) - time(b));
}

function reducer(s: State, a: Action): State {
  switch (a.type) {
    case "page": {
      const runs = { ...s.runs };
      for (const r of a.page.runs ?? []) runs[r.envelope_id] = r;
      return {
        ...s,
        list: upsertAll(s.list, a.page.messages ?? []),
        runs,
        next: a.older || !s.loaded ? a.page.next : s.next,
        loaded: true,
        loadingOlder: a.older ? false : s.loadingOlder,
        error: null,
      };
    }
    case "olderStart":
      return { ...s, loadingOlder: true };
    case "olderFail":
      return { ...s, loadingOlder: false };
    case "error":
      return { ...s, error: a.error, loaded: true };
    case "upsert":
      return { ...s, list: upsertAll(s.list, [a.m]) };
    case "deltas": {
      let hit = false;
      const list = s.list.map((m) => {
        const add = a.d.get(m.id);
        if (add === undefined) return m;
        hit = true;
        return { ...m, body: m.body + add };
      });
      return hit ? { ...s, list } : s;
    }
    case "remove":
      return { ...s, list: s.list.filter((m) => m.id !== a.id) };
    case "run":
      return { ...s, runs: { ...s.runs, [a.line.envelope_id]: a.line } };
  }
}

let localSeq = 0;

/** useChat loads the messages of one chat, follows its stream and sends messages and card answers. */
export function useChat(space: string, chat: string, userId: string) {
  const [s, dispatch] = useReducer(reducer, initial);
  const qc = useQueryClient();
  const stateRef = useRef(s);
  useEffect(() => {
    stateRef.current = s;
  });

  const loadLatest = useCallback(() => {
    fetchMessages(space, chat).then(
      (page) => dispatch({ type: "page", page, older: false }),
      (err) => dispatch({ type: "error", error: errorText(err) }),
    );
  }, [space, chat]);

  useEffect(() => loadLatest(), [loadLatest]);

  const loadOlder = useCallback(() => {
    const cur = stateRef.current;
    if (!cur.next || cur.loadingOlder) return;
    dispatch({ type: "olderStart" });
    fetchMessages(space, chat, cur.next).then(
      (page) => dispatch({ type: "page", page, older: true }),
      (err) => {
        dispatch({ type: "olderFail" });
        toast.error(t("chat.loadOlderFailed"), { description: errorText(err) });
      },
    );
  }, [space, chat]);

  // Token deltas are joined and applied once per animation frame.
  const pending = useRef(new Map<string, string>());
  const frame = useRef<number | null>(null);
  const flush = useCallback(() => {
    if (frame.current !== null) cancelAnimationFrame(frame.current);
    frame.current = null;
    if (pending.current.size === 0) return;
    const d = pending.current;
    pending.current = new Map();
    dispatch({ type: "deltas", d });
  }, []);
  useEffect(
    () => () => {
      if (frame.current !== null) cancelAnimationFrame(frame.current);
    },
    [],
  );

  const handlers = useMemo(
    () => ({
      message: (data: unknown) => {
        flush();
        dispatch({ type: "upsert", m: data as SchemaMessageView });
      },
      delta: (data: unknown) => {
        const { id, text } = data as { id: string; text: string };
        pending.current.set(id, (pending.current.get(id) ?? "") + text);
        if (frame.current === null) frame.current = requestAnimationFrame(flush);
      },
      message_removed: (data: unknown) => {
        flush();
        dispatch({ type: "remove", id: (data as { id: string }).id });
      },
      run: (data: unknown) => {
        const line = data as SchemaRunLine;
        dispatch({ type: "run", line });
        void qc.invalidateQueries({ queryKey: keys.run(space, line.run_id) });
      },
    }),
    [flush, qc, space],
  );

  useEventStream(`/api/v1/spaces/${space}/chats/${chat}/stream`, handlers, loadLatest);

  const send = useCallback(
    (text: string) => {
      const localId = `local-${++localSeq}`;
      const local: SchemaMessageView = {
        id: localId,
        chat_id: chat,
        author_kind: "user",
        author_id: userId,
        body: text,
        created_at: new Date().toISOString(),
        attachments: [],
        reactions: {},
      };
      dispatch({ type: "upsert", m: local });
      postMessage(space, chat, text).then(
        (res) => {
          dispatch({ type: "remove", id: localId });
          dispatch({ type: "upsert", m: res.message });
          if (res.steered) toast(t("chat.steered"));
        },
        (err) => {
          dispatch({ type: "remove", id: localId });
          toast.error(t("chat.sendFailed"), { description: errorText(err) });
        },
      );
    },
    [space, chat, userId],
  );

  const answer = useCallback(
    (cardId: string, a: CardAnswer) => {
      answerCard(space, cardId, a.action as CardAction, a.value).then(
        (m) => dispatch({ type: "upsert", m }),
        (err) => toast.error(t("card.answerFailed"), { description: errorText(err) }),
      );
    },
    [space],
  );

  // UI messages are cached per source object so memoized rows skip unchanged messages.
  const cache = useRef(new Map<string, { src: SchemaMessageView; run?: SchemaRunLine; out: Message }>());
  const messages = useMemo(() => {
    const lastOfEnvelope = new Map<string, string>();
    for (const m of s.list) if (m.envelope_id) lastOfEnvelope.set(m.envelope_id, m.id);
    const next = new Map<string, { src: SchemaMessageView; run?: SchemaRunLine; out: Message }>();
    const out = s.list.map((m) => {
      const run = m.envelope_id && lastOfEnvelope.get(m.envelope_id) === m.id ? s.runs[m.envelope_id] : undefined;
      const hit = cache.current.get(m.id);
      const entry = hit && hit.src === m && hit.run === run ? hit : { src: m, run, out: toMessage(m, run) };
      next.set(m.id, entry);
      return entry.out;
    });
    cache.current = next;
    return out;
  }, [s.list, s.runs]);

  const activeRun = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const r = messages[i].run;
      if (r && LIVE_STATES.has(r.state)) return r;
    }
    return undefined;
  }, [messages]);

  return {
    messages,
    activeRun,
    loaded: s.loaded,
    error: s.error,
    hasOlder: !!s.next,
    loadingOlder: s.loadingOlder,
    loadOlder,
    send,
    answer,
    reload: loadLatest,
  };
}
