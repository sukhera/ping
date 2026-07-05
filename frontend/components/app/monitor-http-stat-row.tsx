import { StatBlock } from "@/components/app/stat-block";
import { computeUptimeWindows } from "@/lib/monitor-uptime";
import type { LatencyPoint, Monitor } from "@/types/monitor";

function pct(value: number | null): string {
  return value === null ? "—" : `${value.toFixed(value < 100 ? 1 : 0)}%`;
}

/** Sample-count-weighted mean of the loaded window's per-bucket averages. */
function avgLatency(points: LatencyPoint[]): number | null {
  const totalSamples = points.reduce((sum, p) => sum + p.sample_count, 0);
  if (totalSamples === 0) return null;
  const weighted = points.reduce((sum, p) => sum + p.avg * p.sample_count, 0);
  return weighted / totalSamples;
}

/** Same 90d daily_stats approximation as the heartbeat stat row — see
 * docs/DEVELOPMENT.md "Known gaps" (no all-time aggregate until PING-020). */
function totalProbes(monitor: Monitor): number | null {
  const stats = monitor.daily_stats ?? [];
  if (stats.length === 0) return null;
  return stats.reduce((sum, s) => sum + s.checkins, 0);
}

export function MonitorHTTPStatRow({
  monitor,
  latencyPoints,
}: {
  monitor: Monitor;
  latencyPoints: LatencyPoint[];
}) {
  const windows = computeUptimeWindows(monitor.daily_stats);
  const avg = avgLatency(latencyPoints);
  const total = totalProbes(monitor);

  return (
    <section aria-label="Monitor stats" className="mb-[22px] grid grid-cols-2 gap-3 sm:grid-cols-4">
      <StatBlock label="Uptime · 7d" value={pct(windows.d7)} />
      <StatBlock label="Uptime · 30d" value={pct(windows.d30)} />
      <StatBlock label="Uptime · 90d" value={pct(windows.d90)} />
      <StatBlock
        label="Avg latency · total probes"
        value={avg !== null ? `${Math.round(avg)}ms` : "—"}
        sub={total !== null ? `${total} probes (last 90d)` : "no data yet"}
      />
    </section>
  );
}
