import { StatBlock } from "@/components/app/stat-block";
import { computeUptimeWindows } from "@/lib/monitor-uptime";
import type { Checkin, Monitor } from "@/types/monitor";

function pct(value: number | null): string {
  return value === null ? "—" : `${value.toFixed(value < 100 ? 1 : 0)}%`;
}

/**
 * Best-effort average runtime: pairs each "start" checkin with the next
 * "success" for the same monitor within the currently loaded log page. This
 * is NOT the monitor's full history — see docs/DEVELOPMENT.md "Known gaps"
 * (no ticket scoped a backend aggregate for this DESIGN.md §7.2 stat).
 */
function avgRuntimeMs(checkins: Checkin[]): number | null {
  // Oldest first so a "start" always pairs with the success that follows it.
  const chronological = [...checkins].sort(
    (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
  );

  const durations: number[] = [];
  let pendingStart: number | null = null;
  for (const c of chronological) {
    if (c.kind === "start") {
      pendingStart = new Date(c.created_at).getTime();
    } else if (c.kind === "success" && pendingStart !== null) {
      durations.push(new Date(c.created_at).getTime() - pendingStart);
      pendingStart = null;
    }
  }
  if (durations.length === 0) return null;
  return durations.reduce((a, b) => a + b, 0) / durations.length;
}

function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const remS = s % 60;
  return remS > 0 ? `${m}m ${remS}s` : `${m}m`;
}

/**
 * Total check-ins summed over the fetched daily_stats window (90d) — an
 * approximation of "total", not a true all-time count once rows age out of
 * retention (PING-020). See docs/DEVELOPMENT.md "Known gaps".
 */
function totalCheckins(monitor: Monitor): number | null {
  const stats = monitor.daily_stats ?? [];
  if (stats.length === 0) return null;
  return stats.reduce((sum, s) => sum + s.checkins, 0);
}

export function MonitorStatRow({
  monitor,
  recentCheckins,
}: {
  monitor: Monitor;
  recentCheckins: Checkin[];
}) {
  const windows = computeUptimeWindows(monitor.daily_stats);
  const runtime = avgRuntimeMs(recentCheckins);
  const total = totalCheckins(monitor);

  return (
    <section aria-label="Monitor stats" className="mb-[22px] grid grid-cols-2 gap-3 sm:grid-cols-4">
      <StatBlock label="Uptime · 7d" value={pct(windows.d7)} />
      <StatBlock label="Uptime · 30d" value={pct(windows.d30)} />
      <StatBlock label="Uptime · 90d" value={pct(windows.d90)} />
      <StatBlock
        label="Avg runtime · total check-ins"
        value={runtime !== null ? formatDuration(runtime) : "—"}
        sub={total !== null ? `${total} check-ins (last 90d)` : "no data yet"}
      />
    </section>
  );
}
