import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "@/api/client";
import { useSession } from "@/api/hooks";

const key = (space: string, part: string) => ["s", space, "learning", part] as const;

export function useSignals(days: number) {
  const space = useSession().space.id;
  return useQuery({
    queryKey: [...key(space, "signals"), days],
    queryFn: async () =>
      (await must(api.GET("/api/v1/spaces/{space}/learning/signals", { params: { path: { space }, query: { days } } })))
        .signals ?? [],
  });
}

export function useScores(days: number) {
  const space = useSession().space.id;
  return useQuery({
    queryKey: [...key(space, "scores"), days],
    queryFn: async () =>
      must(api.GET("/api/v1/spaces/{space}/learning/scores", { params: { path: { space }, query: { days } } })),
  });
}

export function useRouterReport(weeksAgo: number) {
  const space = useSession().space.id;
  return useQuery({
    queryKey: [...key(space, "router"), weeksAgo],
    queryFn: async () =>
      must(
        api.GET("/api/v1/spaces/{space}/learning/router-report", {
          params: { path: { space }, query: { weeks_ago: weeksAgo } },
        }),
      ),
  });
}

export function useRunPatchJob() {
  const space = useSession().space.id;
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () =>
      (await must(api.POST("/api/v1/spaces/{space}/learning/patches/run", { params: { path: { space } } }))).skills ??
      [],
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["s", space, "learning"] }),
  });
}
