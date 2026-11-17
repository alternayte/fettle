import { createContext, useContext } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, must } from "./client";
import type {
  SchemaBotBody,
  SchemaMeView,
  SchemaProviderBody,
  SchemaSpaceView,
  SchemaTarget,
  SchemaVaultBody,
  SchemaRoutineBody,
  SchemaSetWebSearchRequest,
  SchemaSkillView,
  SchemaZoneBody,
} from "./schema";

export type UsageDimension = "run" | "bot" | "zone" | "step" | "role" | "model" | "day" | "month";
export type ModelRole = "owner" | "router" | "verifier" | "computer_use" | "summarizer" | "transcribe";

/** Session holds the caller and the space the UI works in. The layout provides it after /me loads. */
export interface Session {
  me: SchemaMeView;
  space: SchemaSpaceView;
  isOwner: boolean;
  /** chooseSpace switches the UI to another space of the caller. */
  chooseSpace: (id: string) => void;
}

export const SessionContext = createContext<Session | null>(null);

export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error("useSession needs a SessionContext provider");
  return s;
}

function useSpaceId(): string {
  return useSession().space.id;
}

export const keys = {
  me: ["me"] as const,
  space: (s: string) => ["s", s] as const,
  bots: (s: string) => ["s", s, "bots"] as const,
  bot: (s: string, b: string) => ["s", s, "bots", b] as const,
  versions: (s: string, b: string) => ["s", s, "bots", b, "versions"] as const,
  botMemory: (s: string, b: string) => ["s", s, "bots", b, "memory"] as const,
  sharedMemory: (s: string) => ["s", s, "memory"] as const,
  chats: (s: string) => ["s", s, "chats"] as const,
  runs: (s: string) => ["s", s, "runs"] as const,
  run: (s: string, r: string) => ["s", s, "runs", r] as const,
  presets: ["presets"] as const,
  providers: (s: string) => ["s", s, "providers"] as const,
  roles: (s: string, bot?: string) => ["s", s, "roles", bot ?? ""] as const,
  vault: (s: string) => ["s", s, "vault"] as const,
  settings: (s: string) => ["s", s, "settings"] as const,
  settingHistory: (s: string, k: string) => ["s", s, "settings", k, "history"] as const,
  usage: (s: string, by: string, days: number) => ["s", s, "usage", by, days] as const,
  audit: (s: string, action: string) => ["s", s, "audit", action] as const,
  telemetry: ["telemetry"] as const,
  zones: (s: string) => ["s", s, "zones"] as const,
  computerProviders: (s: string) => ["s", s, "computer-providers"] as const,
  policy: (s: string) => ["s", s, "policy"] as const,
  routines: (s: string) => ["s", s, "routines"] as const,
  connectors: (s: string) => ["s", s, "connectors"] as const,
  catalog: ["connector-catalog"] as const,
  routineRuns: (s: string, r: string) => ["s", s, "routines", r, "runs"] as const,
  skills: (s: string) => ["s", s, "skills"] as const,
  skill: (s: string, k: string) => ["s", s, "skills", k] as const,
  files: (s: string, q: string) => ["s", s, "files", q] as const,
  members: (s: string) => ["s", s, "members"] as const,
  invites: (s: string) => ["s", s, "invites"] as const,
  instance: ["instance", "view"] as const,
  adminUsers: ["admin", "users"] as const,
  edition: ["admin", "edition"] as const,
  webSearch: (s: string) => ["s", s, "web-search"] as const,
};

// ---------------------------------------------------------------- me and space

export function useMe() {
  return useQuery({
    queryKey: keys.me,
    queryFn: () => must(api.GET("/api/v1/me")),
    retry: (n, err) => (err as { status?: number }).status !== 401 && n < 2,
  });
}

export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => must(api.PATCH("/api/v1/me", { body: { name } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.me }),
  });
}

export function useUpdateSpace() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: { name?: string; time_zone?: string }) =>
      must(api.PATCH("/api/v1/spaces/{space}", { params: { path: { space } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.me });
      void qc.invalidateQueries({ queryKey: keys.settings(space) });
    },
  });
}

// ---------------------------------------------------------------- bots

export function useBots() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.bots(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/bots", { params: { path: { space } } }))).bots ?? [],
  });
}

export function useBot(bot: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.bot(space, bot),
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/bots/{bot}", { params: { path: { space, bot } } })),
  });
}

export function useUpdateBot(bot: string) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: SchemaBotBody) =>
      must(api.PATCH("/api/v1/spaces/{space}/bots/{bot}", { params: { path: { space, bot } }, body })),
    onSuccess: (v) => {
      qc.setQueryData(keys.bot(space, bot), v);
      void qc.invalidateQueries({ queryKey: keys.bots(space), exact: true });
      void qc.invalidateQueries({ queryKey: keys.versions(space, bot) });
    },
  });
}

export function useInstructionVersions(bot: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.versions(space, bot),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/bots/{bot}/instructions", { params: { path: { space, bot } } })))
        .versions ?? [],
  });
}

export function useRestoreInstructions(bot: string) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (rev: number) =>
      must(
        api.POST("/api/v1/spaces/{space}/bots/{bot}/instructions/{rev}/restore", {
          params: { path: { space, bot, rev } },
        }),
      ),
    onSuccess: (v) => {
      qc.setQueryData(keys.bot(space, bot), v);
      void qc.invalidateQueries({ queryKey: keys.versions(space, bot) });
    },
  });
}

export function useBotMemory(bot: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.botMemory(space, bot),
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/bots/{bot}/memory", { params: { path: { space, bot } } })),
  });
}

export function usePutBotMemory(bot: string) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (content: string) =>
      must(
        api.PUT("/api/v1/spaces/{space}/bots/{bot}/memory", { params: { path: { space, bot } }, body: { content } }),
      ),
    onSuccess: (v) => qc.setQueryData(keys.botMemory(space, bot), v),
  });
}

export function useSharedMemory() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.sharedMemory(space),
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/memory", { params: { path: { space } } })),
  });
}

export function usePutSharedMemory() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (content: string) =>
      must(api.PUT("/api/v1/spaces/{space}/memory", { params: { path: { space } }, body: { content } })),
    onSuccess: (v) => qc.setQueryData(keys.sharedMemory(space), v),
  });
}

export function useCreateBot() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: { name: string; job?: string; zone_id?: string }) =>
      must(api.POST("/api/v1/spaces/{space}/bots", { params: { path: { space } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.bots(space), exact: true });
      void qc.invalidateQueries({ queryKey: keys.chats(space) });
    },
  });
}

/** useTemplates lists the bot templates that ship with Fettle. */
export function useTemplates() {
  return useQuery({
    queryKey: ["templates"],
    queryFn: async () => (await must(api.GET("/api/v1/templates"))).templates ?? [],
    staleTime: Infinity,
  });
}

/** useCreateFromTemplate makes a bot from a template, with its skill and its paused routine. */
export function useCreateFromTemplate() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ slug, zone_id }: { slug: string; zone_id?: string }) =>
      must(
        api.POST("/api/v1/spaces/{space}/templates/{template}", {
          params: { path: { space, template: slug } },
          body: zone_id ? { zone_id } : {},
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.bots(space), exact: true });
      void qc.invalidateQueries({ queryKey: keys.chats(space) });
      void qc.invalidateQueries({ queryKey: ["s", space, "skills"] });
      void qc.invalidateQueries({ queryKey: ["s", space, "routines"] });
    },
  });
}

/** useMoveBotZone moves a bot to another zone (owners). */
export function useMoveBotZone(botId: string) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (zone_id: string) =>
      must(
        api.PUT("/api/v1/spaces/{space}/bots/{bot}/zone", {
          params: { path: { space, bot: botId } },
          body: { zone_id },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.bots(space) });
      void qc.invalidateQueries({ queryKey: keys.zones(space) });
    },
  });
}

// ---------------------------------------------------------------- chats

export function useChats() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.chats(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/chats", { params: { path: { space } } }))).chats ?? [],
  });
}

export function fetchMessages(space: string, chat: string, before?: string) {
  return must(
    api.GET("/api/v1/spaces/{space}/chats/{chat}/messages", {
      params: { path: { space, chat }, query: before ? { before, limit: 50 } : { limit: 50 } },
    }),
  );
}

export type PostOptions = { replyTo?: string; attachments?: string[]; confirmEveryone?: boolean };

export function postMessage(space: string, chat: string, body: string, o: PostOptions = {}) {
  return must(
    api.POST("/api/v1/spaces/{space}/chats/{chat}/messages", {
      params: { path: { space, chat } },
      body: {
        body,
        reply_to: o.replyTo,
        attachments: o.attachments && o.attachments.length > 0 ? o.attachments : undefined,
        confirm_everyone: o.confirmEveryone || undefined,
      },
    }),
  );
}

/** uploadAttachment sends one file for the next message of a chat. */
export async function uploadAttachment(space: string, chat: string, file: File) {
  const res = await fetch(`/api/v1/spaces/${space}/chats/${chat}/attachments?name=${encodeURIComponent(file.name)}`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": file.type || "application/octet-stream" },
    body: file,
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, (body as { detail?: string }).detail ?? `HTTP ${res.status}`);
  return body as { id: string; name: string; size: number; type: string };
}

export function setReaction(space: string, chat: string, message: string, emoji: string) {
  return must(
    api.PUT("/api/v1/spaces/{space}/chats/{chat}/messages/{message}/reaction", {
      params: { path: { space, chat, message } },
      body: { emoji },
    }),
  );
}

export function useCreateGroup() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (b: { name: string; bot_ids: string[] }) =>
      must(api.POST("/api/v1/spaces/{space}/chats", { params: { path: { space } }, body: b })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.chats(space) }),
  });
}

export type CardAction =
  | "allow"
  | "always"
  | "deny"
  | "answer"
  | "continue"
  | "stop"
  | "add_budget"
  | "done"
  | "not_done"
  | "dismiss"
  | "accept"
  | "retry"
  | "pass"
  | "fail"
  | "create"
  | "decline";

export function answerCard(space: string, card: string, action: CardAction, value?: string) {
  return must(
    api.POST("/api/v1/spaces/{space}/cards/{card}/answer", {
      params: { path: { space, card } },
      body: value === undefined ? { action } : { action, value },
    }),
  );
}

// ---------------------------------------------------------------- runs

export const LIVE_STATES = new Set(["queued", "running", "waiting", "verifying"]);

export function useRuns(bot?: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: [...keys.runs(space), bot ?? ""],
    queryFn: async () =>
      (
        await must(
          api.GET("/api/v1/spaces/{space}/runs", {
            params: { path: { space }, query: bot ? { bot, limit: 100 } : { limit: 100 } },
          }),
        )
      ).runs ?? [],
  });
}

export function useRun(run: string | null) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.run(space, run ?? ""),
    enabled: !!run,
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/runs/{run}", { params: { path: { space, run: run! } } })),
    refetchInterval: (q) => (q.state.data && LIVE_STATES.has(q.state.data.line.state) ? 2000 : false),
  });
}

export function useStopRun() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (run: string) =>
      must(api.POST("/api/v1/spaces/{space}/runs/{run}/stop", { params: { path: { space, run } } })),
    onSuccess: (_, run) => {
      void qc.invalidateQueries({ queryKey: keys.run(space, run) });
      void qc.invalidateQueries({ queryKey: keys.runs(space), exact: false });
    },
  });
}

export function useRerun() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: async (run: string) =>
      (await must(api.POST("/api/v1/spaces/{space}/runs/{run}/rerun", { params: { path: { space, run } } }))).run_id,
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.runs(space), exact: false }),
  });
}

// ---------------------------------------------------------------- models

export function usePresets() {
  return useQuery({
    queryKey: keys.presets,
    queryFn: async () => (await must(api.GET("/api/v1/model-presets"))).presets ?? [],
    staleTime: Infinity,
  });
}

export function useProviders() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.providers(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/model-providers", { params: { path: { space } } }))).providers ?? [],
  });
}

export function useCreateProvider() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: SchemaProviderBody) =>
      must(api.POST("/api/v1/spaces/{space}/model-providers", { params: { path: { space } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.providers(space) }),
  });
}

export function useUpdateProvider() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: SchemaProviderBody }) =>
      must(
        api.PATCH("/api/v1/spaces/{space}/model-providers/{provider}", {
          params: { path: { space, provider: id } },
          body,
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.providers(space) }),
  });
}

export function useDeleteProvider() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (id: string) =>
      must(
        api.DELETE("/api/v1/spaces/{space}/model-providers/{provider}", { params: { path: { space, provider: id } } }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.providers(space) });
      void qc.invalidateQueries({ queryKey: ["s", space, "roles"] });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

export function useTestProvider() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationKey: ["testProvider"],
    mutationFn: ({ id, model }: { id: string; model?: string }) =>
      must(
        api.POST("/api/v1/spaces/{space}/model-providers/{provider}/test", {
          params: { path: { space, provider: id } },
          body: model ? { model } : {},
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.providers(space) });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

export function useRoles(bot?: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.roles(space, bot),
    queryFn: async () =>
      (
        await must(
          api.GET("/api/v1/spaces/{space}/model-roles", { params: { path: { space }, query: bot ? { bot } : {} } }),
        )
      ).roles ?? [],
  });
}

export function useSetRole() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ role, targets, bot }: { role: ModelRole; targets: SchemaTarget[]; bot?: string }) =>
      must(
        api.PUT("/api/v1/spaces/{space}/model-roles/{role}", {
          params: { path: { space, role } },
          body: bot ? { bot_id: bot, targets } : { targets },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["s", space, "roles"] });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

// ---------------------------------------------------------------- vault

export function useVault() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.vault(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/vault", { params: { path: { space } } }))).items ?? [],
  });
}

export function useSaveVaultItem() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, body }: { id?: string; body: SchemaVaultBody }) =>
      id
        ? must(api.PATCH("/api/v1/spaces/{space}/vault/{item}", { params: { path: { space, item: id } }, body }))
        : must(api.POST("/api/v1/spaces/{space}/vault", { params: { path: { space } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.vault(space) }),
  });
}

export function useDeleteVaultItem() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (id: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/vault/{item}", { params: { path: { space, item: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.vault(space) }),
  });
}

// ---------------------------------------------------------------- settings, usage, audit, telemetry

export function useSettings() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.settings(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/settings", { params: { path: { space } } }))).settings ?? [],
  });
}

export function useSetSetting() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ key, value }: { key: string; value: unknown }) =>
      must(api.PUT("/api/v1/spaces/{space}/settings/{key}", { params: { path: { space, key } }, body: { value } })),
    onSuccess: (_, { key }) => {
      void qc.invalidateQueries({ queryKey: keys.settings(space) });
      void qc.invalidateQueries({ queryKey: keys.settingHistory(space, key) });
    },
  });
}

export function useSettingHistory(key: string | null) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.settingHistory(space, key ?? ""),
    enabled: !!key,
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/settings/{key}/history", { params: { path: { space, key: key! } } })))
        .history ?? [],
  });
}

export function useUsage(by: UsageDimension, days: number) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.usage(space, by, days),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/usage", { params: { path: { space }, query: { by, days } } })))
        .rows ?? [],
    placeholderData: keepPreviousData,
  });
}

export function useAudit(action: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.audit(space, action),
    queryFn: async () =>
      (
        await must(
          api.GET("/api/v1/spaces/{space}/audit", {
            params: { path: { space }, query: action ? { action, limit: 500 } : { limit: 500 } },
          }),
        )
      ).entries ?? [],
    placeholderData: keepPreviousData,
  });
}

export function useTelemetry() {
  return useQuery({ queryKey: keys.telemetry, queryFn: () => must(api.GET("/api/v1/admin/telemetry")) });
}

export function useSetTelemetry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) => must(api.PUT("/api/v1/admin/telemetry", { body: { enabled } })),
    onSuccess: (v) => qc.setQueryData(keys.telemetry, v),
  });
}

// ---------------------------------------------------------------- computers and policy

export function useZones() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.zones(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/zones", { params: { path: { space } } }))).zones ?? [],
    refetchInterval: 5000,
  });
}

export function useComputerProviders() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.computerProviders(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/computer-providers", { params: { path: { space } } }))).providers ??
      [],
  });
}

/** useCreateZone asks for a zone. The zone exists after a person allows the card. */
export function useCreateZone() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: SchemaZoneBody) =>
      must(api.POST("/api/v1/spaces/{space}/zones", { params: { path: { space } }, body })),
  });
}

export function useUpdateZone() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: SchemaZoneBody }) =>
      must(api.PATCH("/api/v1/spaces/{space}/zones/{zone}", { params: { path: { space, zone: id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.zones(space) }),
  });
}

/** useTestZoneComputer starts or wakes a zone's computer and runs one command in it. */
export function useTestZoneComputer() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (zone: string) =>
      must(api.POST("/api/v1/spaces/{space}/zones/{zone}/computer/test", { params: { path: { space, zone } } })),
    onSettled: () => qc.invalidateQueries({ queryKey: keys.zones(space) }),
  });
}

/** useSleepZoneComputer puts a zone's computer to sleep; its files stay. */
export function useSleepZoneComputer() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (zone: string) =>
      must(api.POST("/api/v1/spaces/{space}/zones/{zone}/computer/stop", { params: { path: { space, zone } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.zones(space) }),
  });
}

/** useEnableHost asks to turn on the host provider. It is on after a person allows the card. */
export function useEnableHost() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: () =>
      must(api.POST("/api/v1/spaces/{space}/computer-providers/host/enable", { params: { path: { space } } })),
  });
}

export function usePolicy() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.policy(space),
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/policy", { params: { path: { space } } })),
  });
}

/** useSetMode asks to change the approval mode. It changes after a person allows the card. */
export function useSetMode() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: (mode: "ask" | "auto" | "never") =>
      must(api.PUT("/api/v1/spaces/{space}/approval-mode", { params: { path: { space } }, body: { mode } })),
  });
}

export function useAddRule() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: {
      tool?: string;
      site_pattern?: string;
      recipient_pattern?: string;
      effect: "allow" | "ask" | "block";
    }) => must(api.POST("/api/v1/spaces/{space}/policy/rules", { params: { path: { space } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.policy(space) }),
  });
}

export function useDeleteRule() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (id: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/policy/rules/{rule}", { params: { path: { space, rule: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.policy(space) }),
  });
}

/** useAnswerChange answers a hard-ask change card from a settings screen. */
export function useAnswerChange() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ card, action }: { card: string; action: "allow" | "deny" }) => answerCard(space, card, action),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.zones(space) });
      void qc.invalidateQueries({ queryKey: keys.computerProviders(space) });
      void qc.invalidateQueries({ queryKey: keys.policy(space) });
      void qc.invalidateQueries({ queryKey: keys.settings(space) });
    },
  });
}

// ---------------------------------------------------------------- routines

export function useRoutines() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.routines(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/routines", { params: { path: { space } } }))).routines ?? [],
  });
}

export function useRoutineRuns(routine: string | null) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.routineRuns(space, routine ?? ""),
    enabled: !!routine,
    queryFn: async () =>
      (
        await must(
          api.GET("/api/v1/spaces/{space}/routines/{routine}/runs", { params: { path: { space, routine: routine! } } }),
        )
      ).runs ?? [],
  });
}

export function useSaveRoutine() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, body }: { id?: string; body: SchemaRoutineBody }) =>
      id
        ? must(
            api.PATCH("/api/v1/spaces/{space}/routines/{routine}", { params: { path: { space, routine: id } }, body }),
          ).then((routine) => ({ routine, secret: undefined as string | undefined }))
        : must(api.POST("/api/v1/spaces/{space}/routines", { params: { path: { space } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.routines(space) }),
  });
}

export function useDeleteRoutine() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (id: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/routines/{routine}", { params: { path: { space, routine: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.routines(space) }),
  });
}

export function useRunRoutine() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (id: string) =>
      must(api.POST("/api/v1/spaces/{space}/routines/{routine}/run", { params: { path: { space, routine: id } } })),
    onSuccess: (_, id) => qc.invalidateQueries({ queryKey: keys.routineRuns(space, id) }),
  });
}

// ---------------------------------------------------------------- skills

export function useSkills() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.skills(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/skills", { params: { path: { space } } }))).skills ?? [],
  });
}

export function useSkill(skill: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.skill(space, skill),
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/skills/{skill}", { params: { path: { space, skill } } })),
  });
}

export function useSaveSkill() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (skillMD: string) =>
      must(api.POST("/api/v1/spaces/{space}/skills", { params: { path: { space } }, body: { skill_md: skillMD } })),
    onSuccess: (sk) => {
      void qc.invalidateQueries({ queryKey: keys.skills(space) });
      void qc.invalidateQueries({ queryKey: keys.skill(space, sk.id) });
    },
  });
}

export function useImportSkill() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: async (file: File) => {
      const res = await fetch(`/api/v1/spaces/${space}/skills/import`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/zip" },
        body: file,
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new ApiError(res.status, (body as { detail?: string }).detail ?? `HTTP ${res.status}`);
      return body as SchemaSkillView;
    },
    onSuccess: (sk) => {
      void qc.invalidateQueries({ queryKey: keys.skills(space) });
      void qc.invalidateQueries({ queryKey: keys.skill(space, sk.id) });
    },
  });
}

export function useDeleteSkill() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (skill: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/skills/{skill}", { params: { path: { space, skill } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.skills(space) }),
  });
}

export function useAssignSkill() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ skill, bot, on }: { skill: string; bot: string; on: boolean }) =>
      must(
        api.PUT("/api/v1/spaces/{space}/skills/{skill}/bots/{bot}", {
          params: { path: { space, skill, bot } },
          body: { on },
        }),
      ),
    onSuccess: (_, { skill }) => {
      void qc.invalidateQueries({ queryKey: keys.skills(space) });
      void qc.invalidateQueries({ queryKey: keys.skill(space, skill) });
    },
  });
}

export function useSyncSkael() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: () => must(api.POST("/api/v1/spaces/{space}/skills/sync", { params: { path: { space } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.skills(space) }),
  });
}

export function useDraftSkill() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: async (run: string) =>
      must(api.POST("/api/v1/spaces/{space}/runs/{run}/draft-skill", { params: { path: { space, run } } })),
  });
}

// ---------------------------------------------------------------- bot lifecycle

function useBotAction<T>(fn: (space: string, bot: string) => Promise<T>) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (bot: string) => fn(space, bot),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.bots(space) });
      void qc.invalidateQueries({ queryKey: keys.chats(space) });
      void qc.invalidateQueries({ queryKey: keys.routines(space) });
    },
  });
}

export const useDuplicateBot = () =>
  useBotAction((space, bot) =>
    must(api.POST("/api/v1/spaces/{space}/bots/{bot}/duplicate", { params: { path: { space, bot } } })),
  );
export const useArchiveBot = () =>
  useBotAction((space, bot) =>
    must(api.POST("/api/v1/spaces/{space}/bots/{bot}/archive", { params: { path: { space, bot } } })),
  );
export const useRestoreBot = () =>
  useBotAction((space, bot) =>
    must(api.POST("/api/v1/spaces/{space}/bots/{bot}/restore", { params: { path: { space, bot } } })),
  );
export const useMakePrimary = () =>
  useBotAction((space, bot) =>
    must(api.POST("/api/v1/spaces/{space}/bots/{bot}/primary", { params: { path: { space, bot } } })),
  );

export function useDeleteBot() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ bot, confirm }: { bot: string; confirm: string }) =>
      must(api.DELETE("/api/v1/spaces/{space}/bots/{bot}", { params: { path: { space, bot }, query: { confirm } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.bots(space) });
      void qc.invalidateQueries({ queryKey: keys.chats(space) });
    },
  });
}

// ---------------------------------------------------------------- screens

export function useOpenScreen() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: (bot: string) =>
      must(api.POST("/api/v1/spaces/{space}/bots/{bot}/screen", { params: { path: { space, bot } } })),
  });
}

export function useControlScreen() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: (bot: string) =>
      must(api.POST("/api/v1/spaces/{space}/bots/{bot}/screen/control", { params: { path: { space, bot } } })),
  });
}

export function useScreenDone() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ bot, card, note }: { bot: string; card?: string; note?: string }) =>
      must(
        api.POST("/api/v1/spaces/{space}/bots/{bot}/screen/done", {
          params: { path: { space, bot } },
          body: { card_id: card, note },
        }),
      ),
  });
}

// ---------------------------------------------------------------- teaching (§13.3)

const teachKey = (s: string, id: string) => ["s", s, "teach", id] as const;

export function useTeachReady(bot: string, enabled = true) {
  const space = useSpaceId();
  return useQuery({
    queryKey: ["s", space, "bots", bot, "teach-ready"],
    enabled,
    staleTime: 0,
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/bots/{bot}/teach-ready", { params: { path: { space, bot } } })),
  });
}

export function useStartTeach() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: { bot_id: string; title?: string; takeover_card?: string; change_size?: boolean }) =>
      must(api.POST("/api/v1/spaces/{space}/teach", { params: { path: { space } }, body })),
  });
}

/** useTeach polls a session while it records or waits for the bot. */
export function useTeach(id: string) {
  const space = useSpaceId();
  return useQuery({
    queryKey: teachKey(space, id),
    queryFn: () => must(api.GET("/api/v1/spaces/{space}/teach/{teach}", { params: { path: { space, teach: id } } })),
    refetchInterval: (q) => {
      const st = q.state.data?.status;
      if (st === "recording") return 1500;
      if (st === "asking" || st === "drafting") return 3000;
      return false;
    },
  });
}

function useTeachMutation<V, R>(fn: (space: string, id: string, v: V) => Promise<R>, id: string) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (v: V) => fn(space, id, v),
    onSuccess: () => qc.invalidateQueries({ queryKey: teachKey(space, id) }),
  });
}

export function useTeachNote(id: string) {
  return useTeachMutation(
    (space, teach, body: { text: string; new_step?: boolean }) =>
      must(api.POST("/api/v1/spaces/{space}/teach/{teach}/notes", { params: { path: { space, teach } }, body })),
    id,
  );
}

export function useTeachVoiceNote(id: string) {
  return useTeachMutation(async (space, teach, audio: Blob) => {
    const res = await fetch(`/api/v1/spaces/${space}/teach/${teach}/notes/audio`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": audio.type || "application/octet-stream" },
      body: audio,
    });
    if (!res.ok) {
      const p = (await res.json().catch(() => ({}))) as { detail?: string };
      throw new ApiError(res.status, p.detail ?? `HTTP ${res.status}`);
    }
    return res.json();
  }, id);
}

export function useTeachDone(id: string) {
  return useTeachMutation(
    (space, teach) =>
      must(api.POST("/api/v1/spaces/{space}/teach/{teach}/done", { params: { path: { space, teach } } })),
    id,
  );
}

export type TeachEdit = {
  skill_md?: string;
  inputs?: { name: string; description: string; example?: string }[];
  evals_json?: string;
  steps?: { seq: number; value?: string; command?: string; notes?: string[]; vault_item?: string; removed?: boolean }[];
};

export function useTeachEdit(id: string) {
  return useTeachMutation(
    (space, teach, body: TeachEdit) =>
      must(api.PUT("/api/v1/spaces/{space}/teach/{teach}/draft", { params: { path: { space, teach } }, body })),
    id,
  );
}

export function useTeachSave(id: string) {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: TeachEdit) =>
      must(api.POST("/api/v1/spaces/{space}/teach/{teach}/save", { params: { path: { space, teach: id } }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: teachKey(space, id) });
      void qc.invalidateQueries({ queryKey: keys.skills(space) });
    },
  });
}

export function useTeachDiscard(id: string) {
  return useTeachMutation(
    (space, teach) =>
      must(api.POST("/api/v1/spaces/{space}/teach/{teach}/discard", { params: { path: { space, teach } } })),
    id,
  );
}

export function useTeachRetry(id: string) {
  return useTeachMutation(
    (space, teach) =>
      must(api.POST("/api/v1/spaces/{space}/teach/{teach}/retry", { params: { path: { space, teach } } })),
    id,
  );
}

export function useTryTeach(id: string) {
  return useTeachMutation(
    (space, teach, inputs: Record<string, string>) =>
      must(
        api.POST("/api/v1/spaces/{space}/teach/{teach}/try", { params: { path: { space, teach } }, body: { inputs } }),
      ),
    id,
  );
}

// ---------------------------------------------------------------- connectors

export function useConnectors() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.connectors(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/connectors", { params: { path: { space } } }))).connectors ?? [],
  });
}

export function useCatalog() {
  return useQuery({
    queryKey: keys.catalog,
    staleTime: Infinity,
    queryFn: async () => (await must(api.GET("/api/v1/connectors/catalog"))).connectors ?? [],
  });
}

export type NewConnector = {
  name: string;
  url?: string;
  kind?: "mcp_remote" | "mcp_stdio";
  command?: string[];
  auth?: "none" | "oauth" | "header";
  header_name?: string;
  icon?: string;
};

export function useCreateConnector() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: NewConnector) =>
      must(api.POST("/api/v1/spaces/{space}/connectors", { params: { path: { space } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.connectors(space) }),
  });
}

export function useDeleteConnector() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (id: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/connectors/{connector}", { params: { path: { space, connector: id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.connectors(space) }),
  });
}

export function useAddAccount() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, label, value }: { id: string; label: string; value?: string }) =>
      must(
        api.POST("/api/v1/spaces/{space}/connectors/{connector}/accounts", {
          params: { path: { space, connector: id } },
          body: { label, value },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.connectors(space) }),
  });
}

export function useAuthorize() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, label }: { id: string; label: string }) =>
      must(
        api.POST("/api/v1/spaces/{space}/connectors/{connector}/authorize", {
          params: { path: { space, connector: id } },
          body: { label },
        }),
      ),
  });
}

export function useTestConnector() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, account }: { id: string; account: string }) =>
      must(
        api.POST("/api/v1/spaces/{space}/connectors/{connector}/test", {
          params: { path: { space, connector: id } },
          body: { account_id: account },
        }),
      ),
  });
}

export function useSetTool() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ id, tool, cls, disabled }: { id: string; tool: string; cls?: string; disabled: boolean }) =>
      must(
        api.PUT("/api/v1/spaces/{space}/connectors/{connector}/tools/{tool}", {
          params: { path: { space, connector: id, tool } },
          body: { class: cls as "read" | undefined, disabled },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.connectors(space) }),
  });
}

export function useAddComposio() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (key: string) =>
      must(api.POST("/api/v1/spaces/{space}/connectors/composio", { params: { path: { space } }, body: { key } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.connectors(space) }),
  });
}

// ---------------------------------------------------------------- files

const FILES_PAGE = 50;

/** useFiles lists the files that bots sent, newest first, one page at a time. */
export function useFiles(q: string) {
  const space = useSpaceId();
  return useInfiniteQuery({
    queryKey: keys.files(space, q),
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      must(
        api.GET("/api/v1/spaces/{space}/files", {
          params: {
            path: { space },
            query: { limit: FILES_PAGE, ...(q ? { q } : {}), ...(pageParam ? { before: pageParam } : {}) },
          },
        }),
      ),
    getNextPageParam: (last) => last.next || undefined,
    placeholderData: keepPreviousData,
  });
}

// ---------------------------------------------------------------- members and invites

export function useMembers() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.members(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/members", { params: { path: { space } } }))).members ?? [],
  });
}

export function useSetMemberRole() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: ({ user, role }: { user: string; role: "owner" | "member" }) =>
      must(api.PUT("/api/v1/spaces/{space}/members/{user}", { params: { path: { space, user } }, body: { role } })),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: keys.members(space) });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

export function useRemoveMember() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (user: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/members/{user}", { params: { path: { space, user } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.members(space) }),
  });
}

export function useInvites(enabled: boolean) {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.invites(space),
    enabled,
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/invites", { params: { path: { space } } }))).invites ?? [],
  });
}

export function useCreateInvite() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: { role: "owner" | "member"; days: number }) =>
      must(api.POST("/api/v1/spaces/{space}/invites", { params: { path: { space } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.invites(space) }),
  });
}

export function useRevokeInvite() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (invite: string) =>
      must(api.DELETE("/api/v1/spaces/{space}/invites/{invite}", { params: { path: { space, invite } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.invites(space) }),
  });
}

// ---------------------------------------------------------------- web search

/** useWebSearch returns the search provider of the space, or null when web_search is off. */
export function useWebSearch() {
  const space = useSpaceId();
  return useQuery({
    queryKey: keys.webSearch(space),
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/web-search", { params: { path: { space } } }))).provider ?? null,
  });
}

export function useSetWebSearch() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: (body: SchemaSetWebSearchRequest) =>
      must(api.PUT("/api/v1/spaces/{space}/web-search", { params: { path: { space } }, body })),
    onSuccess: (out) => qc.setQueryData(keys.webSearch(space), out.provider ?? null),
  });
}

export function useDeleteWebSearch() {
  const qc = useQueryClient();
  const space = useSpaceId();
  return useMutation({
    mutationFn: () => must(api.DELETE("/api/v1/spaces/{space}/web-search", { params: { path: { space } } })),
    onSuccess: () => qc.setQueryData(keys.webSearch(space), null),
  });
}

export function useTestWebSearch() {
  const space = useSpaceId();
  return useMutation({
    mutationFn: (query: string) =>
      must(api.POST("/api/v1/spaces/{space}/web-search/test", { params: { path: { space } }, body: { query } })),
  });
}

// ---------------------------------------------------------------- instance and admin

export function useInstance() {
  return useQuery({ queryKey: keys.instance, queryFn: () => must(api.GET("/api/v1/instance")), staleTime: 60_000 });
}

export function useAdminUsers(enabled: boolean) {
  return useQuery({
    queryKey: keys.adminUsers,
    enabled,
    queryFn: async () => (await must(api.GET("/api/v1/admin/users"))).users ?? [],
  });
}

export function useEdition(enabled: boolean) {
  return useQuery({ queryKey: keys.edition, enabled, queryFn: () => must(api.GET("/api/v1/admin/edition")) });
}

export function useDiagnostics() {
  return useMutation({
    mutationFn: async () => (await must(api.GET("/api/v1/admin/diagnostics"))).checks ?? [],
  });
}
