import { Skeleton } from "@/components/ui/skeleton";
import { StatusChip } from "@/components/app/status-chip";
import { formatRelativeTime } from "@/lib/format-relative-time";
import type { MonitorEvent } from "@/types/monitor";

// Timeline event types map onto the same status shapes as StatusChip where
// there's a natural fit (state transitions); everything else (pause, mute,
// config_change, ...) gets the neutral "paused" shape rather than inventing a
// new glyph per event type.
const EVENT_SHAPE: Record<string, "up" | "down" | "late"> = {
  up: "up",
  down: "down",
  late: "late",
};

function shapeFor(type: string): "up" | "down" | "late" | "paused" {
  return EVENT_SHAPE[type] ?? "paused";
}

function EventRow({ event }: { event: MonitorEvent }) {
  return (
    <div role="row" className="flex items-start gap-3 border-b border-border px-4 py-2.5 last:border-b-0">
      <span className="mt-0.5">
        <StatusChip state={shapeFor(event.type)} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-[13px] text-text">{event.message}</div>
        <div className="mono mt-0.5 text-[11.5px] text-text-faint" title={event.created_at}>
          {formatRelativeTime(event.created_at)} ago · {event.type}
        </div>
      </div>
    </div>
  );
}

export function MonitorEventFeedLoading() {
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface">
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="border-b border-border px-4 py-2.5 last:border-b-0">
          <Skeleton className="h-4 w-56" />
        </div>
      ))}
    </div>
  );
}

export function MonitorEventFeedEmpty() {
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-4 py-10 text-center text-[13px] text-text-faint">
      No events yet.
    </div>
  );
}

export function MonitorEventFeed({ events }: { events: MonitorEvent[] }) {
  if (events.length === 0) return <MonitorEventFeedEmpty />;

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface" role="table" aria-label="Event feed">
      {events.map((e) => (
        <EventRow key={e.id} event={e} />
      ))}
    </div>
  );
}
