import { useCallback, useEffect, useMemo, useState } from "react";
import { Shell } from "@/app/Shell";
import { Sidebar, type NavTarget } from "@/app/Sidebar";
import { CommandPalette, useHotkey } from "@/app/CommandPalette";
import { ChatView } from "@/features/chat/ChatView";
import type { CardAnswer } from "@/features/chat/CardView";
import type { Message } from "@/features/chat/types";
import { Timeline } from "@/features/runs/Timeline";
import * as fx from "./fixtures";

// PreviewChat drives the real chat components with fixture data and a simulated stream.
export function PreviewChat() {
  const [messages, setMessages] = useState<Message[]>(fx.messages);
  const [panelRun, setPanelRun] = useState<string | null>(() => (window.innerWidth >= 1024 ? "r-desks" : null));
  const [menu, setMenu] = useState(false);
  const [palette, setPalette] = useState(false);
  const [active, setActive] = useState<NavTarget>({ kind: "chat", chatId: "c-chief" });
  const bots = useMemo(() => Object.fromEntries(fx.bots.map((b) => [b.id, b])), []);
  useHotkey(
    "k",
    useCallback(() => setPalette((o) => !o), []),
  );

  useEffect(() => {
    const id = "m-stream";
    const words = fx.streamingReply.split(/(?<=\s)/);
    let i = 0;
    setMessages((ms) => [
      ...ms.filter((m) => m.id !== id),
      {
        id,
        chat_id: "c-chief",
        author_kind: "bot",
        author_id: "b-chief",
        body: "",
        created_at: new Date().toISOString(),
        streaming: true,
      },
    ]);
    const timer = setInterval(() => {
      i++;
      setMessages((ms) =>
        ms.map((m) => (m.id === id ? { ...m, body: words.slice(0, i).join(""), streaming: i < words.length } : m)),
      );
      if (i >= words.length) clearInterval(timer);
    }, 45);
    return () => clearInterval(timer);
  }, []);

  const onAnswer = (cardId: string, a: CardAnswer) =>
    setMessages((ms) =>
      ms.map((m) =>
        m.card?.id === cardId
          ? {
              ...m,
              card: {
                ...m.card,
                status: a.action === "deny" ? "denied" : a.action === "answer" ? "answered" : "allowed",
                answer: a.value,
              },
              run: m.run ? { ...m.run, state: a.action === "deny" ? "cancelled" : "running" } : m.run,
            }
          : m,
      ),
    );

  const onSend = (text: string) =>
    setMessages((ms) => [
      ...ms,
      {
        id: `u-${ms.length}`,
        chat_id: "c-chief",
        author_kind: "user",
        author_id: "u-owner",
        body: text,
        created_at: new Date().toISOString(),
      },
    ]);

  const sidebar = (
    <Sidebar
      spaceName="acme"
      bots={fx.bots}
      chats={fx.chats}
      active={active}
      onNavigate={(to) => {
        setActive(to);
        setMenu(false);
      }}
      onPalette={() => setPalette(true)}
      isOwner
    />
  );
  const streaming = messages.some((m) => m.streaming);
  return (
    <>
      <Shell
        sidebar={sidebar}
        menuOpen={menu}
        onMenuOpenChange={setMenu}
        panel={panelRun ? <Timeline run={fx.run} onClose={() => setPanelRun(null)} /> : null}
      >
        <ChatView
          title="Chief"
          subtitle={bots["b-chief"].job}
          bot={bots["b-chief"]}
          bots={bots}
          messages={messages}
          running={streaming}
          onSend={onSend}
          onStop={() => undefined}
          onAnswer={onAnswer}
          onOpenRun={(id) => setPanelRun((cur) => (cur === id ? null : id))}
          activeRunId={panelRun ?? undefined}
          onMenu={() => setMenu(true)}
          onPanel={() => setPanelRun((cur) => (cur ? null : "r-desks"))}
          panelOpen={!!panelRun}
        />
      </Shell>
      <CommandPalette
        open={palette}
        onOpenChange={setPalette}
        bots={fx.bots}
        chats={fx.chats}
        onNavigate={setActive}
        isOwner
      />
    </>
  );
}
