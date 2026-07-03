import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  listMonitors,
  muteMonitor,
  pauseMonitor,
  resumeMonitor,
  unmuteMonitor,
} from "@/lib/api";
import type { Monitor, MonitorListParams, MonitorListResponse } from "@/types/monitor";

export const monitorKeys = {
  all: ["monitors"] as const,
  list: (params: MonitorListParams) => ["monitors", "list", params] as const,
};

/**
 * Polls the monitor list every 30s (DESIGN.md §7.1). Callers with identical
 * params (e.g. the sidebar's global summary and the dashboard's unfiltered
 * stat strip both calling useMonitors({})) share one cached query and one
 * network request — no lifting/context needed, TanStack Query dedupes by key.
 */
export function useMonitors(params: MonitorListParams) {
  return useQuery({
    queryKey: monitorKeys.list(params),
    queryFn: ({ signal }) => listMonitors(params, signal),
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
    placeholderData: (prev) => prev,
  });
}

/**
 * Shared plumbing for pause/resume/mute/unmute: the mutation response is
 * already the authoritative updated Monitor, so we write it directly into
 * every cached list query (setQueriesData, partial key match on ["monitors"])
 * instead of invalidating and paying for a refetch round-trip.
 */
function useMonitorMutation(fn: (id: string) => Promise<Monitor>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: (updated) => {
      queryClient.setQueriesData<MonitorListResponse | undefined>(
        { queryKey: monitorKeys.all },
        (old) =>
          old && {
            ...old,
            monitors: old.monitors.map((m) => (m.id === updated.id ? updated : m)),
          },
      );
    },
  });
}

export function usePauseMonitor() {
  return useMonitorMutation(pauseMonitor);
}

export function useResumeMonitor() {
  return useMonitorMutation(resumeMonitor);
}

export function useMuteMonitor() {
  return useMonitorMutation(muteMonitor);
}

export function useUnmuteMonitor() {
  return useMonitorMutation(unmuteMonitor);
}
