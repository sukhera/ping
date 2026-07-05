"use client";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatRelativeTime } from "@/lib/format-relative-time";
import { cn } from "@/lib/utils";
import type { ProbeResult } from "@/types/monitor";

function ProbeRow({ probe }: { probe: ProbeResult }) {
  return (
    <div role="row" className="border-b border-border px-4 py-2.5 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]">
        <span className="mono text-text-faint" title={probe.created_at}>
          {formatRelativeTime(probe.created_at)} ago
        </span>
        <span className={cn("mono", probe.ok ? "text-up" : "text-down")}>
          {probe.ok ? "ok" : "fail"}
        </span>
        {probe.http_status !== undefined && (
          <span className="mono text-text-faint">{probe.http_status}</span>
        )}
        {probe.latency_ms !== undefined && (
          <span className="mono text-text-faint">{probe.latency_ms}ms</span>
        )}
      </div>
      {probe.error && (
        // Same escaping guarantee as CheckinLog's body preview: JSX text
        // children only, never dangerouslySetInnerHTML, so a hostile target
        // response reflected into `error` renders inert.
        <pre className="mono mt-1 max-w-full overflow-x-auto text-wrap break-all whitespace-pre-wrap text-[12px] text-down">
          {probe.error}
        </pre>
      )}
    </div>
  );
}

export function ProbeLogLoading() {
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="border-b border-border px-4 py-2.5 last:border-b-0">
          <Skeleton className="h-4 w-64" />
        </div>
      ))}
    </div>
  );
}

export function ProbeLogEmpty() {
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-4 py-10 text-center text-[13px] text-text-faint">
      No probes yet.
    </div>
  );
}

export function ProbeLog({
  probes,
  hasMore,
  onLoadMore,
  loadingMore,
}: {
  probes: ProbeResult[];
  hasMore: boolean;
  onLoadMore: () => void;
  loadingMore: boolean;
}) {
  if (probes.length === 0) return <ProbeLogEmpty />;

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface" role="table" aria-label="Probe log">
      {probes.map((p) => (
        <ProbeRow key={p.id} probe={p} />
      ))}
      {hasMore && (
        <div className="flex justify-center border-t border-border p-2">
          <Button variant="outline" size="sm" onClick={onLoadMore} disabled={loadingMore}>
            {loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </div>
  );
}
