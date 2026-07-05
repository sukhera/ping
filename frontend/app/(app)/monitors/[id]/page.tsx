"use client";

import Link from "next/link";
import { useParams } from "next/navigation";

import { CheckinLog, CheckinLogEmpty, CheckinLogLoading } from "@/components/app/checkin-log";
import { HowToPing } from "@/components/app/how-to-ping";
import { MonitorDetailHeader } from "@/components/app/monitor-detail-header";
import {
  MonitorEventFeed,
  MonitorEventFeedEmpty,
  MonitorEventFeedLoading,
} from "@/components/app/monitor-event-feed";
import { MonitorStatRow } from "@/components/app/monitor-stat-row";
import { UptimeBar } from "@/components/app/uptime-bar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useMonitor,
  useMonitorCheckins,
  useMonitorEvents,
} from "@/hooks/use-monitors";

function DetailLoading() {
  return (
    <div>
      <div className="mb-[22px] flex items-center gap-3">
        <Skeleton className="size-3.5 rounded-full" />
        <Skeleton className="h-7 w-48" />
      </div>
      <div className="mb-[22px] grid grid-cols-4 gap-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-[72px]" />
        ))}
      </div>
      <Skeleton className="mb-[22px] h-[52px] w-full" />
    </div>
  );
}

function DetailError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
      <p className="text-down">Couldn&apos;t load this monitor.</p>
      <Button variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

function NotFound() {
  return (
    <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
      <p className="text-text">Monitor not found.</p>
      <Button asChild variant="outline">
        <Link href="/dashboard">Back to dashboard</Link>
      </Button>
    </div>
  );
}

const CHECKIN_PAGE_SIZE = 20;

export default function MonitorDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;

  const monitorQuery = useMonitor(id);
  const checkinsQuery = useMonitorCheckins(id, { limit: CHECKIN_PAGE_SIZE });
  const eventsQuery = useMonitorEvents(id, { limit: 20 });

  if (monitorQuery.isLoading) return <DetailLoading />;
  if (monitorQuery.isError) {
    const status = (monitorQuery.error as { status?: number } | undefined)?.status;
    if (status === 404 || status === 403) return <NotFound />;
    return <DetailError onRetry={() => monitorQuery.refetch()} />;
  }

  const monitor = monitorQuery.data;
  if (!monitor) return <NotFound />;

  const checkins = checkinsQuery.data?.pages.flatMap((p) => p.checkins) ?? [];
  const events = eventsQuery.data?.events ?? [];

  return (
    <div>
      <MonitorDetailHeader monitor={monitor} />
      <MonitorStatRow monitor={monitor} recentCheckins={checkins} />

      <div className="mb-[22px]">
        <UptimeBar dailyStats={monitor.daily_stats} />
      </div>

      <HowToPing monitor={monitor} className="mb-[22px]" />

      <div className="mb-[22px]">
        <h2 className="mb-3 text-sm font-medium text-text">Check-in log</h2>
        {checkinsQuery.isLoading ? (
          <CheckinLogLoading />
        ) : checkinsQuery.isError ? (
          <CheckinLogEmpty />
        ) : (
          <CheckinLog
            checkins={checkins}
            hasMore={checkinsQuery.hasNextPage}
            loadingMore={checkinsQuery.isFetchingNextPage}
            onLoadMore={() => checkinsQuery.fetchNextPage()}
          />
        )}
      </div>

      <div>
        <h2 className="mb-3 text-sm font-medium text-text">Events</h2>
        {eventsQuery.isLoading ? (
          <MonitorEventFeedLoading />
        ) : eventsQuery.isError ? (
          <MonitorEventFeedEmpty />
        ) : (
          <MonitorEventFeed events={events} />
        )}
      </div>
    </div>
  );
}
