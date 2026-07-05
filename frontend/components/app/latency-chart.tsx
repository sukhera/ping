"use client";

import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import type { LatencyPoint, LatencyWindow, ProbeResult } from "@/types/monitor";

const WINDOWS: { value: LatencyWindow; label: string }[] = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

function formatTick(iso: string, window: LatencyWindow): string {
  const d = new Date(iso);
  if (window === "24h") {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function TooltipContent({
  active,
  payload,
  window,
}: {
  active?: boolean;
  payload?: { payload: LatencyPoint }[];
  window: LatencyWindow;
}) {
  if (!active || !payload?.length) return null;
  const point = payload[0]?.payload;
  if (!point) return null;
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-3 py-2 text-[12px] shadow-lg">
      <div className="mono text-text-faint">{formatTick(point.bucket_start, window)}</div>
      <div className="mono text-text">avg {Math.round(point.avg)}ms</div>
      <div className="mono text-text-dim">p95 {Math.round(point.p95)}ms</div>
    </div>
  );
}

export function LatencyChartLoading() {
  return <Skeleton className="h-[220px] w-full" />;
}

export function LatencyChartEmpty() {
  return (
    <div className="flex h-[220px] items-center justify-center rounded-[var(--radius)] border border-border bg-surface text-[13px] text-text-faint">
      No latency data yet.
    </div>
  );
}

/**
 * DESIGN.md §7.2 HTTP detail latency chart: area (avg latency, --up fill
 * fading to transparent) with failed probes plotted as --down dots along the
 * same x-axis. Latency series only aggregates successful probes (backend
 * excludes failures from percentiles), so failure markers come from the
 * separately-fetched probe log, matched onto the chart's time domain.
 */
export function LatencyChart({
  points,
  failedProbes,
  window,
  onWindowChange,
}: {
  points: LatencyPoint[];
  failedProbes: ProbeResult[];
  window: LatencyWindow;
  onWindowChange: (window: LatencyWindow) => void;
}) {
  if (points.length === 0) {
    return (
      <div>
        <WindowToggle window={window} onWindowChange={onWindowChange} />
        <LatencyChartEmpty />
      </div>
    );
  }

  const domainStart = new Date(points[0]!.bucket_start).getTime();
  const lastBucketStart = new Date(points[points.length - 1]!.bucket_start).getTime();
  // Each point is a bucket's *start*, so the domain actually extends one
  // bucket further than the last point's timestamp — without this, a
  // failure inside the still-filling final bucket (e.g. one probed 30s ago,
  // in a 5min bucket) falls just past domainEnd and silently disappears.
  const bucketWidth =
    points.length > 1 ? lastBucketStart - new Date(points[points.length - 2]!.bucket_start).getTime() : 0;
  const domainEnd = lastBucketStart + bucketWidth;
  const maxLatency = Math.max(...points.map((p) => p.p95), 1);

  const failureMarkers = failedProbes
    .map((p) => ({ x: new Date(p.created_at).getTime(), y: maxLatency * 1.05 }))
    .filter((m) => m.x >= domainStart && m.x <= domainEnd);

  return (
    <div>
      <WindowToggle window={window} onWindowChange={onWindowChange} />
      <div
        className="relative h-[220px] w-full rounded-[var(--radius)] border border-border bg-surface p-2"
        role="img"
        aria-label={`Latency chart, ${window} window, ${failureMarkers.length} failures`}
      >
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={points} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
            <defs>
              <linearGradient id="latencyFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--up)" stopOpacity={0.35} />
                <stop offset="100%" stopColor="var(--up)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="bucket_start"
              tickFormatter={(v: string) => formatTick(v, window)}
              stroke="var(--text-faint)"
              tick={{ fontSize: 11, fill: "var(--text-faint)" }}
              tickLine={false}
              axisLine={{ stroke: "var(--border)" }}
              minTickGap={40}
            />
            <YAxis
              stroke="var(--text-faint)"
              tick={{ fontSize: 11, fill: "var(--text-faint)" }}
              tickLine={false}
              axisLine={false}
              width={40}
              tickFormatter={(v: number) => `${v}ms`}
            />
            <Tooltip content={<TooltipContent window={window} />} />
            <Area
              type="monotone"
              dataKey="avg"
              stroke="var(--up)"
              strokeWidth={1.5}
              fill="url(#latencyFill)"
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
        {failureMarkers.length > 0 && (
          <div className="pointer-events-none absolute inset-2">
            <ResponsiveContainer width="100%" height="100%">
              <ScatterChart margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
                <XAxis
                  type="number"
                  dataKey="x"
                  domain={[domainStart, domainEnd]}
                  hide
                />
                <YAxis type="number" dataKey="y" domain={[0, maxLatency * 1.1]} hide width={40} />
                <Scatter data={failureMarkers} fill="var(--down)" />
              </ScatterChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>
    </div>
  );
}

function WindowToggle({
  window,
  onWindowChange,
}: {
  window: LatencyWindow;
  onWindowChange: (window: LatencyWindow) => void;
}) {
  return (
    <div className="mb-2 flex items-center justify-end gap-1" role="group" aria-label="Chart window">
      {WINDOWS.map((w) => (
        <Button
          key={w.value}
          type="button"
          variant="outline"
          size="sm"
          className={cn(w.value === window && "border-accent text-accent")}
          aria-pressed={w.value === window}
          onClick={() => onWindowChange(w.value)}
        >
          {w.label}
        </Button>
      ))}
    </div>
  );
}
