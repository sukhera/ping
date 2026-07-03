import Link from "next/link";

import { Logo } from "@/components/app/logo";
import { MonitorRow, ROW_GRID_CLASS } from "@/components/app/monitor-row";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import type { Monitor } from "@/types/monitor";

const HEADERS = ["Status", "Name", "Kind", "Schedule", "Last check-in", "90 days", "Uptime", "Actions"];
// Visually empty in the mockup (status icon / ⋯ menu need no printed label),
// but empty-table-header (axe) requires header cells to have accessible
// text — sr-only keeps the pixel layout identical while giving screen
// readers a name for these two columns.
const VISUALLY_HIDDEN_HEADERS = new Set(["Status", "Actions"]);

function BoardShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface" role="table" aria-label="Monitor list">
      <div
        role="row"
        className={cn(
          ROW_GRID_CLASS,
          "border-b border-border py-2.5 text-[10.5px] tracking-[0.08em] text-text-faint uppercase",
        )}
      >
        {HEADERS.map((h, i) => (
          // sr-only is position:absolute, which would pull this cell out of
          // the grid entirely and shift every later column left by one track
          // — so the grid cell itself always stays a normal flow participant,
          // and only an inner span (not a grid item) gets visually hidden.
          <span role="columnheader" key={i} className={i === 6 ? "text-right" : undefined}>
            <span className={VISUALLY_HIDDEN_HEADERS.has(h) ? "sr-only" : undefined}>{h}</span>
          </span>
        ))}
      </div>
      {children}
    </div>
  );
}

export function MonitorBoardLoading() {
  return (
    <BoardShell>
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className={cn(ROW_GRID_CLASS, "border-b border-border last:border-b-0")}>
          <Skeleton className="size-2.5 rounded-full" />
          <Skeleton className="h-4 w-32" />
          <Skeleton className="h-4 w-12" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-4 w-16" />
          <Skeleton className="h-3.5 w-full" />
          <Skeleton className="ml-auto h-4 w-10" />
          <span />
        </div>
      ))}
    </BoardShell>
  );
}

export function MonitorBoardError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
      <p className="text-down">Couldn&apos;t load monitors.</p>
      <Button variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

export function MonitorBoardEmpty() {
  return (
    <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
      <Logo showWordmark={false} className="opacity-60" />
      <p className="text-text">No monitors yet.</p>
      <code className="mono rounded-[var(--radius)] border border-border bg-surface-2 px-3 py-1.5 text-xs text-text-dim">
        curl https://ping.example.com/p/demo
      </code>
      <Button asChild>
        <Link href="/monitors/new">+ New monitor</Link>
      </Button>
    </div>
  );
}

export function MonitorBoardNoMatches({ onClear }: { onClear: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-surface py-20 text-center">
      <p className="text-text">No monitors match your search.</p>
      <Button variant="outline" onClick={onClear}>
        Clear filters
      </Button>
    </div>
  );
}

export function MonitorBoard({ monitors }: { monitors: Monitor[] }) {
  return (
    <BoardShell>
      {monitors.map((m) => (
        <MonitorRow key={m.id} monitor={m} />
      ))}
    </BoardShell>
  );
}
