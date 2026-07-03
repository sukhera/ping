import Link from "next/link";
import { useEffect, useRef, useState } from "react";

import { StatusChip } from "@/components/app/status-chip";
import { UptimeBar } from "@/components/app/uptime-bar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  useMuteMonitor,
  usePauseMonitor,
  useResumeMonitor,
  useUnmuteMonitor,
} from "@/hooks/use-monitors";
import { formatRelativeTime } from "@/lib/format-relative-time";
import { cn } from "@/lib/utils";
import type { Monitor } from "@/types/monitor";

// Row grid mirrors design-mockup.html's `.row`/`.thead`:
// status | name+slug | kind | schedule | last check-in | 90-day bar | uptime% | menu
export const ROW_GRID_CLASS =
  "grid grid-cols-[16px_minmax(180px,1.4fr)_84px_1fr_110px_300px_120px_28px] items-center gap-3.5 px-[18px] py-3.5";

function uptimePct(monitor: Monitor): string {
  const stats = monitor.daily_stats ?? [];
  if (monitor.display_state === "paused" || stats.length === 0) return "—";
  const totalChecks = stats.reduce((sum, s) => sum + s.checkins, 0);
  const totalFailures = stats.reduce((sum, s) => sum + s.failures, 0);
  if (totalChecks === 0) return "—";
  return `${(((totalChecks - totalFailures) / totalChecks) * 100).toFixed(totalFailures ? 1 : 0)}%`;
}

function lastCheckinText(monitor: Monitor): { text: string; className: string } {
  if (monitor.display_state === "paused") {
    return {
      text: monitor.paused_at ? `paused ${formatRelativeTime(monitor.paused_at)} ago` : "paused",
      className: "text-text-faint",
    };
  }
  if (monitor.display_state === "down") {
    return {
      text: monitor.last_checkin_at
        ? `missed · ${formatRelativeTime(monitor.last_checkin_at)}`
        : "missed",
      className: "text-down",
    };
  }
  if (monitor.display_state === "late") {
    return {
      text: monitor.last_checkin_at
        ? `late · ${formatRelativeTime(monitor.last_checkin_at)} into grace`
        : "late",
      className: "text-late",
    };
  }
  return {
    text: monitor.last_checkin_at ? `${formatRelativeTime(monitor.last_checkin_at)} ago` : "never",
    className: "text-text-dim",
  };
}

/**
 * Tracks whether this row's last_checkin_at just changed (a fresh check-in
 * arrived on the latest 30s poll), so StatusChip can pulse. Comparison
 * happens in an effect (refs/setState are only restricted during render, not
 * inside effects); the "no pulse on first mount" guard is the ref starting
 * unset. Once true, isFresh is never reset back to false — that's fine
 * because the pulse ring is keyed by last_checkin_at (status-chip.tsx) and
 * is a one-shot (non-infinite) CSS animation, so it only ever replays when
 * the key actually changes, regardless of isFresh's stale value in between.
 */
function useIsFreshCheckin(lastCheckinAt: string | undefined): boolean {
  const prevRef = useRef<string | undefined>(undefined);
  const [isFresh, setIsFresh] = useState(false);

  useEffect(() => {
    const prev = prevRef.current;
    prevRef.current = lastCheckinAt;
    if (lastCheckinAt && prev && prev !== lastCheckinAt) {
      setIsFresh(true);
    }
  }, [lastCheckinAt]);

  return isFresh;
}

export function MonitorRow({ monitor }: { monitor: Monitor }) {
  const pause = usePauseMonitor();
  const resume = useResumeMonitor();
  const mute = useMuteMonitor();
  const unmute = useUnmuteMonitor();
  const isFresh = useIsFreshCheckin(monitor.last_checkin_at);

  const last = lastCheckinText(monitor);
  const isPaused = monitor.display_state === "paused";
  const isDown = monitor.display_state === "down";

  return (
    <div
      role="row"
      className={cn(
        "relative border-b border-border last:border-b-0 hover:bg-surface-2",
        ROW_GRID_CLASS,
        isDown && "bg-down/5 shadow-[inset_3px_0_0_var(--down)]",
      )}
    >
      {/* Full-bleed invisible link so the whole row is clickable, while the
          ⋯ menu (an interactive element) stays outside it — a <tr>/<a> can't
          nest interactive elements, so this overlay + z-10 trigger pattern
          is used instead of literally wrapping the row in <a>. Wrapped in a
          role="cell" <span> (rather than putting role="cell" on the <a>
          itself, which axe's aria-allowed-role flags as invalid) so
          role="row"'s aria-required-children is still satisfied. */}
      <span role="cell" className="contents">
        <Link
          href={`/monitors/${monitor.id}`}
          className="absolute inset-0"
          aria-label={`View ${monitor.name}`}
        />
      </span>

      {/* role="row" only permits role="cell"/"gridcell" children (ARIA
          aria-required-children) — every visual column below is wrapped
          accordingly, matching the mockup's div-grid layout otherwise as-is. */}
      <span role="cell">
        <StatusChip state={monitor.display_state} pulse={isFresh} pulseKey={monitor.last_checkin_at} />
      </span>

      <span role="cell" className="relative min-w-0">
        <span className="block truncate text-[13.5px] leading-tight font-medium text-text">
          {monitor.name}
        </span>
        <span className="mono block truncate text-[11.5px] text-text-faint">
          {monitor.kind === "heartbeat" ? monitor.ping_url : `${monitor.method} ${monitor.url}`}
        </span>
      </span>

      <span
        role="cell"
        className="mono w-fit rounded border border-border px-1.5 py-0.5 text-center text-[11px] text-text-dim"
      >
        {monitor.kind === "heartbeat" ? "⌁ beat" : "⇄ http"}
      </span>

      <span role="cell" className="truncate text-[12.5px] text-text-dim">
        {monitor.schedule_summary ?? "—"}
      </span>

      <span role="cell" className={cn("mono truncate text-[12px] whitespace-nowrap", last.className)}>
        {last.text}
      </span>

      <span role="cell" className="block">
        <UptimeBar dailyStats={monitor.daily_stats} />
      </span>

      <span
        role="cell"
        className={cn(
          "mono text-right text-[12.5px] whitespace-nowrap",
          isPaused && "text-text-faint",
        )}
      >
        {uptimePct(monitor)}
      </span>

      <span role="cell">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              className="relative z-10 text-center text-text-faint hover:text-text"
              aria-label={`Actions for ${monitor.name}`}
            >
              ⋯
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {isPaused ? (
              <DropdownMenuItem onSelect={() => resume.mutate(monitor.id)}>Resume</DropdownMenuItem>
            ) : (
              <DropdownMenuItem onSelect={() => pause.mutate(monitor.id)}>Pause</DropdownMenuItem>
            )}
            {monitor.alerts_muted ? (
              <DropdownMenuItem onSelect={() => unmute.mutate(monitor.id)}>
                Unmute alerts
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem onSelect={() => mute.mutate(monitor.id)}>Mute alerts</DropdownMenuItem>
            )}
            <DropdownMenuItem asChild>
              <Link href={`/monitors/${monitor.id}`}>View details</Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </div>
  );
}
