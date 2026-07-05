import type { DailyStat } from "@/types/monitor";

export type UptimeWindows = {
  d7: number | null;
  d30: number | null;
  d90: number | null;
};

/**
 * Uptime % for the last N days (inclusive of today), computed the same way
 * monitor-row.tsx's overall uptime% is: (checkins - failures) / checkins. A
 * window is null when no daily_stats row in it has any check-ins yet (the
 * table is unpopulated until PING-020's rollup job ships) — render "-" rather
 * than a misleading 0%/100%.
 */
function uptimeForWindow(dailyStats: DailyStat[], days: number): number | null {
  const cutoff = new Date();
  cutoff.setHours(0, 0, 0, 0);
  cutoff.setDate(cutoff.getDate() - (days - 1));
  const cutoffKey = cutoff.toISOString().slice(0, 10);

  let checkins = 0;
  let failures = 0;
  for (const stat of dailyStats) {
    if (stat.day < cutoffKey) continue;
    checkins += stat.checkins;
    failures += stat.failures;
  }
  if (checkins === 0) return null;
  return ((checkins - failures) / checkins) * 100;
}

export function computeUptimeWindows(dailyStats: DailyStat[] = []): UptimeWindows {
  return {
    d7: uptimeForWindow(dailyStats, 7),
    d30: uptimeForWindow(dailyStats, 30),
    d90: uptimeForWindow(dailyStats, 90),
  };
}
