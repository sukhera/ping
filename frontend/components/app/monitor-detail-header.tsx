"use client";

import Link from "next/link";

import { StatusChip } from "@/components/app/status-chip";
import { Button } from "@/components/ui/button";
import {
  useMuteMonitor,
  usePauseMonitor,
  useResumeMonitor,
  useUnmuteMonitor,
} from "@/hooks/use-monitors";
import { formatRelativeTime } from "@/lib/format-relative-time";
import type { Monitor } from "@/types/monitor";

const STATE_WORD: Record<Monitor["display_state"], string> = {
  up: "UP",
  down: "DOWN",
  late: "LATE",
  new: "NEW",
  paused: "PAUSED",
};

function sinceText(monitor: Monitor): string {
  if (monitor.display_state === "paused" && monitor.paused_at) {
    return `paused ${formatRelativeTime(monitor.paused_at)} ago`;
  }
  if (monitor.last_checkin_at) {
    return `since ${new Date(monitor.last_checkin_at).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    })}`;
  }
  return "no check-ins yet";
}

/**
 * DESIGN.md §7.2 header: status shape + name + big state word + since-when,
 * plus pause/mute/edit actions. Pause/resume/mute/unmute reuse the same
 * optimistic hooks the dashboard row uses (hooks/use-monitors.ts).
 */
export function MonitorDetailHeader({ monitor }: { monitor: Monitor }) {
  const pause = usePauseMonitor();
  const resume = useResumeMonitor();
  const mute = useMuteMonitor();
  const unmute = useUnmuteMonitor();

  const isPaused = monitor.display_state === "paused";

  return (
    <div className="mb-[22px] flex flex-wrap items-start justify-between gap-4">
      <div className="flex items-center gap-3">
        <StatusChip state={monitor.display_state} size="lg" />
        <div>
          <div className="flex items-baseline gap-2.5">
            <h1 className="text-xl font-semibold text-text">{monitor.name}</h1>
            <span className="mono text-sm font-medium text-text-dim">
              {STATE_WORD[monitor.display_state]}
            </span>
          </div>
          <div className="mt-0.5 text-[12.5px] text-text-faint">{sinceText(monitor)}</div>
        </div>
      </div>

      <div className="flex items-center gap-2">
        {isPaused ? (
          <Button variant="outline" size="sm" onClick={() => resume.mutate(monitor.id)}>
            Resume
          </Button>
        ) : (
          <Button variant="outline" size="sm" onClick={() => pause.mutate(monitor.id)}>
            Pause
          </Button>
        )}
        {monitor.alerts_muted ? (
          <Button variant="outline" size="sm" onClick={() => unmute.mutate(monitor.id)}>
            Unmute alerts
          </Button>
        ) : (
          <Button variant="outline" size="sm" onClick={() => mute.mutate(monitor.id)}>
            Mute alerts
          </Button>
        )}
        <Button asChild variant="outline" size="sm">
          <Link href={`/monitors/${monitor.id}/edit`}>Edit</Link>
        </Button>
      </div>
    </div>
  );
}
