"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatRelativeTime } from "@/lib/format-relative-time";
import { cn } from "@/lib/utils";
import type { Checkin } from "@/types/monitor";

const KIND_LABEL: Record<Checkin["kind"], string> = {
  success: "success",
  start: "start",
  fail: "fail",
};

const KIND_CLASS: Record<Checkin["kind"], string> = {
  success: "text-up",
  start: "text-text-dim",
  fail: "text-down",
};

function BodyPreview({ body }: { body: string }) {
  const [expanded, setExpanded] = useState(false);
  const isLong = body.length > 120;
  const preview = isLong && !expanded ? `${body.slice(0, 120)}…` : body;

  return (
    <div className="mt-1">
      {/* React escapes text children by default (no dangerouslySetInnerHTML
          anywhere here), so an HTML/script check-in body renders as inert
          text — this is the only thing standing between a hostile ping
          payload and the dashboard, so it must never become innerHTML. */}
      <pre className="mono max-w-full overflow-x-auto text-wrap break-all whitespace-pre-wrap text-[12px] text-text-dim">
        {preview}
      </pre>
      {isLong && (
        <button
          type="button"
          className="mt-1 text-[11.5px] text-text-faint underline hover:text-text-dim"
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      )}
    </div>
  );
}

function CheckinRow({ checkin }: { checkin: Checkin }) {
  return (
    <div role="row" className="border-b border-border px-4 py-2.5 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]">
        <span className="mono text-text-faint" title={checkin.created_at}>
          {formatRelativeTime(checkin.created_at)} ago
        </span>
        <span className={cn("mono", KIND_CLASS[checkin.kind])}>{KIND_LABEL[checkin.kind]}</span>
        {checkin.source_ip && <span className="mono text-text-faint">{checkin.source_ip}</span>}
        {checkin.user_agent && (
          <span className="truncate text-text-faint" title={checkin.user_agent}>
            {checkin.user_agent}
          </span>
        )}
      </div>
      {checkin.body && <BodyPreview body={checkin.body} />}
    </div>
  );
}

export function CheckinLogLoading() {
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

export function CheckinLogEmpty() {
  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface px-4 py-10 text-center text-[13px] text-text-faint">
      No check-ins yet.
    </div>
  );
}

export function CheckinLog({
  checkins,
  hasMore,
  onLoadMore,
  loadingMore,
}: {
  checkins: Checkin[];
  hasMore: boolean;
  onLoadMore: () => void;
  loadingMore: boolean;
}) {
  if (checkins.length === 0) return <CheckinLogEmpty />;

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface" role="table" aria-label="Check-in log">
      {checkins.map((c) => (
        <CheckinRow key={c.id} checkin={c} />
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
