import { createContext, useContext } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "./client";
import type {
  SchemaBotBody,
  SchemaMeView,
  SchemaProviderBody,
  SchemaSpaceView,
  SchemaTarget,
  SchemaVaultBody,
  SchemaZoneBody,
} from "./schema";

export type UsageDimension = "run" | "bot" | "step" | "role" | "model" | "day" | "month";
export type ModelRole = "owner" | "router" | "verifier" | "computer_use" | "summarizer";

/** Session holds the caller and the space the UI works in. The layout provides it after /me loads. */
export interface Session {
  me: SchemaMeView;
  space: SchemaSpaceView;
  isOwner: boolean;
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

export function postMessage(space: string, chat: string, body: string) {
  return must(
    api.POST("/api/v1/spaces/{space}/chats/{chat}/messages", { params: { path: { space, chat } }, body: { body } }),
  );
}

export type CardAction =
  "allow" | "always" | "deny" | "answer" | "continue" | "stop" | "add_budget" | "done" | "not_done" | "dismiss";

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
