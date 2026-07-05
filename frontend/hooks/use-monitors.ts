import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  getMonitor,
  listMonitorCheckins,
  listMonitorEvents,
  listMonitors,
  muteMonitor,
  pauseMonitor,
  resumeMonitor,
  unmuteMonitor,
} from "@/lib/api";
import type {
  CheckinListParams,
  EventListParams,
  Monitor,
  MonitorDisplayState,
  MonitorListParams,
  MonitorListResponse,
} from "@/types/monitor";

export const monitorKeys = {
  all: ["monitors"] as const,
  list: (params: MonitorListParams) => ["monitors", "list", params] as const,
  detail: (id: string) => ["monitors", "detail", id] as const,
  checkins: (id: string, params: CheckinListParams) => ["monitors", id, "checkins", params] as const,
  events: (id: string, params: EventListParams) => ["monitors", id, "events", params] as const,
};

/** Monitor detail page (PING-014): polls every 30s like the dashboard list. */
export function useMonitor(id: string) {
  return useQuery({
    queryKey: monitorKeys.detail(id),
    queryFn: ({ signal }) => getMonitor(id, signal),
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
    placeholderData: (prev) => prev,
  });
}

/**
 * Check-in log (PING-014), "load more"-paginated: each page's next_cursor
 * feeds the next page's ?cursor, same opaque cursor the events feed already
 * uses. limit is fixed per page (the initial params.limit), not re-read per
 * page — callers wanting a different page size pass it once here.
 */
export function useMonitorCheckins(id: string, params: CheckinListParams = {}) {
  const { limit } = params;
  return useInfiniteQuery({
    queryKey: monitorKeys.checkins(id, { limit }),
    queryFn: ({ pageParam, signal }) => listMonitorCheckins(id, { limit, cursor: pageParam }, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
  });
}

export function useMonitorEvents(id: string, params: EventListParams = {}) {
  return useQuery({
    queryKey: monitorKeys.events(id, params),
    queryFn: ({ signal }) => listMonitorEvents(id, params, signal),
    placeholderData: (prev) => prev,
  });
}

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

/** Writes one updated Monitor into every cached list query and its own detail
 * query — shared by both the optimistic onMutate patch and the real
 * onSuccess write-through below. Skips any cache entry that isn't a
 * MonitorListResponse/Monitor shape (partial key matching on ["monitors"]
 * also reaches unrelated shapes like checkins/events pages). */
function patchMonitorCaches(
  queryClient: ReturnType<typeof useQueryClient>,
  id: string,
  apply: (m: Monitor) => Monitor,
) {
  queryClient.setQueriesData<MonitorListResponse | undefined>(
    { queryKey: monitorKeys.all },
    (old) =>
      old && "monitors" in old
        ? { ...old, monitors: old.monitors.map((m) => (m.id === id ? apply(m) : m)) }
        : old,
  );
  queryClient.setQueryData<Monitor | undefined>(monitorKeys.detail(id), (old) =>
    old ? apply(old) : old,
  );
}

/**
 * Shared plumbing for pause/resume/mute/unmute (AC: "act optimistically and
 * reconcile with server state"): onMutate immediately patches every cached
 * list/detail entry via `optimisticPatch` so the UI reacts before the network
 * round-trip, onSuccess overwrites that guess with the server's authoritative
 * Monitor, and onError rolls back to the pre-mutation snapshot if the request
 * fails — reconciling with server state either way.
 */
function useMonitorMutation(
  fn: (id: string) => Promise<Monitor>,
  optimisticPatch: (m: Monitor) => Monitor,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onMutate: async (id: string) => {
      await queryClient.cancelQueries({ queryKey: monitorKeys.all });
      const snapshot = queryClient.getQueriesData<MonitorListResponse | Monitor | undefined>({
        queryKey: monitorKeys.all,
      });
      patchMonitorCaches(queryClient, id, optimisticPatch);
      return { snapshot };
    },
    onError: (_err, _id, context) => {
      context?.snapshot.forEach(([key, data]) => queryClient.setQueryData(key, data));
    },
    onSuccess: (updated) => {
      patchMonitorCaches(queryClient, updated.id, () => updated);
    },
  });
}

// Optimistic guesses for each action — deliberately conservative (e.g. resume
// guesses "up" since that's what a healthy resume produces; the onSuccess
// write-through overwrites this with the server's actual post-resume state,
// which may re-evaluate to late/down instead).
const PAUSED: MonitorDisplayState = "paused";

export function usePauseMonitor() {
  return useMonitorMutation(pauseMonitor, (m) => ({
    ...m,
    display_state: PAUSED,
    paused_at: m.paused_at ?? new Date().toISOString(),
  }));
}

export function useResumeMonitor() {
  return useMonitorMutation(resumeMonitor, (m) => ({
    ...m,
    display_state: "up",
    paused_at: undefined,
  }));
}

export function useMuteMonitor() {
  return useMonitorMutation(muteMonitor, (m) => ({ ...m, alerts_muted: true }));
}

export function useUnmuteMonitor() {
  return useMonitorMutation(unmuteMonitor, (m) => ({ ...m, alerts_muted: false }));
}
