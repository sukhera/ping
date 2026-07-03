import { formatRelativeTime } from "@/lib/format-relative-time";
import type { Monitor } from "@/types/monitor";

export type MonitorStats = {
  up: number;
  down: number;
  late: number;
  /** null when no monitor in the set has any daily_stats data yet (table is
   * unpopulated until PING-020's rollup job ships) — render "-" rather than
   * a misleading 0%/100%. */
  uptimePct: number | null;
  /** Name + time-since of the first down monitor, for the stat strip's
   * sub-line (mockup: "nightly-backup - 42m"). Undefined when nothing is down. */
  downSample?: { name: string; since: string };
};

export function computeStats(monitors: Monitor[]): MonitorStats {
  let up = 0;
  let down = 0;
  let late = 0;
  let downSample: MonitorStats["downSample"];

  let totalChecks = 0;
  let totalFailures = 0;
  let hasAnyData = false;

  for (const m of monitors) {
    switch (m.display_state) {
      case "up":
        up++;
        break;
      case "down":
        down++;
        if (!downSample) {
          downSample = {
            name: m.name,
            since: m.last_checkin_at ? formatRelativeTime(m.last_checkin_at) : "-",
          };
        }
        break;
      case "late":
        late++;
        break;
      default:
        break;
    }

    for (const stat of m.daily_stats ?? []) {
      hasAnyData = true;
      totalChecks += stat.checkins;
      totalFailures += stat.failures;
    }
  }

  const uptimePct =
    hasAnyData && totalChecks > 0
      ? ((totalChecks - totalFailures) / totalChecks) * 100
      : null;

  return { up, down, late, uptimePct, downSample };
}
