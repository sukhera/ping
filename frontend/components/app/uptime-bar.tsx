import type { DailyStat } from "@/types/monitor";

// DESIGN.md §8: "3×14px rounded rects with 2px gaps".
const CELL_WIDTH = 3;
const CELL_GAP = 2;
const CELL_HEIGHT = 14;
const DEFAULT_DAYS = 90;

type CellStatus = "no-data" | "clean" | "degraded" | "incident";

function classify(stat: DailyStat | undefined): CellStatus {
  if (!stat || stat.checkins === 0) return "no-data";
  if (stat.downtime_s > 0) return "incident";
  // TODO: revisit thresholds once real daily_stats data exists (PING-020) —
  // no live data to validate against yet, this is the least-surprising
  // mapping of the three numeric columns onto three visual severities.
  if (stat.failures > 0) return "degraded";
  return "clean";
}

function fillFor(status: CellStatus): { fill: string; fillOpacity?: number } {
  switch (status) {
    case "clean":
      return { fill: "var(--up)", fillOpacity: 0.6 };
    case "degraded":
      return { fill: "var(--late)" };
    case "incident":
      return { fill: "var(--down)" };
    case "no-data":
    default:
      return { fill: "var(--border)" };
  }
}

function dayKey(d: Date): string {
  return d.toISOString().slice(0, 10);
}

/**
 * DESIGN.md §8 signature element: 90 cells, most recent right, --up at 60%
 * opacity for clean days, --late/--down full opacity for degraded/incident,
 * --border for no-data. Table is empty until PING-020's rollup job ships, so
 * every cell renders no-data today — that's correct, not a bug.
 */
export function UptimeBar({
  dailyStats = [],
  days = DEFAULT_DAYS,
}: {
  dailyStats?: DailyStat[];
  days?: number;
}) {
  const byDay = new Map(dailyStats.map((s) => [s.day, s]));

  const today = new Date();
  today.setHours(0, 0, 0, 0);

  const cells = Array.from({ length: days }, (_, i) => {
    const offset = days - 1 - i;
    const date = new Date(today);
    date.setDate(date.getDate() - offset);
    const key = dayKey(date);
    const stat = byDay.get(key);
    const status = classify(stat);
    const { fill, fillOpacity } = fillFor(status);
    const label =
      status === "no-data"
        ? `${date.toLocaleDateString()}: no data`
        : `${date.toLocaleDateString()}: ${stat!.checkins - stat!.failures}/${stat!.checkins} checks ok`;
    return { key, x: i * (CELL_WIDTH + CELL_GAP), fill, fillOpacity, label };
  });

  const naturalWidth = days * (CELL_WIDTH + CELL_GAP) - CELL_GAP;

  return (
    <svg
      // 90 real cells at DESIGN.md's 3px/2px sizing (448px) are wider than
      // the row grid's 300px uptime-bar column, so the SVG scales to fill
      // its container width via viewBox rather than rendering at native
      // size — preserveAspectRatio="none" lets it stretch to exactly fill
      // the grid track instead of scaling by min(x,y) and letterboxing.
      width="100%"
      height={CELL_HEIGHT}
      viewBox={`0 0 ${naturalWidth} ${CELL_HEIGHT}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={`uptime last ${days} days`}
    >
      {cells.map((cell) => (
        <rect
          key={cell.key}
          x={cell.x}
          y={0}
          width={CELL_WIDTH}
          height={CELL_HEIGHT}
          rx={1.5}
          fill={cell.fill}
          fillOpacity={cell.fillOpacity}
        >
          <title>{cell.label}</title>
        </rect>
      ))}
    </svg>
  );
}
