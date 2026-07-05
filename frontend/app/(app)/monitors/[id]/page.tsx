"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";

import { CheckinLog, CheckinLogEmpty, CheckinLogLoading } from "@/components/app/checkin-log";
import { HowToPing } from "@/components/app/how-to-ping";
import { HTTPConfigSummary } from "@/components/app/http-config-summary";
import { LatencyChart, LatencyChartLoading } from "@/components/app/latency-chart";
import { MonitorDetailHeader } from "@/components/app/monitor-detail-header";
import {
  MonitorEventFeed,
  MonitorEventFeedEmpty,
  MonitorEventFeedLoading,
} from "@/components/app/monitor-event-feed";
import { MonitorHTTPStatRow } from "@/components/app/monitor-http-stat-row";
import { MonitorStatRow } from "@/components/app/monitor-stat-row";
import { ProbeLog, ProbeLogEmpty, ProbeLogLoading } from "@/components/app/probe-log";
import { TLSExpiryNote } from "@/components/app/tls-expiry-note";
import { UptimeBar } from "@/components/app/uptime-bar";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useMonitor,
  useMonitorCheckins,
  useMonitorEvents,
  useMonitorLatency,
  useMonitorProbeResults,
} from "@/hooks/use-monitors";
import { latestTLSExpiry } from "@/lib/tls-expiry";
import type { LatencyWindow, Monitor, ProbeResult } from "@/types/monitor";

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
const PROBE_PAGE_SIZE = 20;

/**
 * DESIGN.md §7.2 HTTP detail: stat row, latency chart, probe log, TLS expiry
 * note, config summary. Owns the latency-window toggle and the probe-results
 * query so the stat row's avg-latency figure and the chart/TLS-note below
 * stay in sync off one fetch each — TLS expiry has no dedicated backend field
 * (see lib/tls-expiry.ts) so it's derived from whichever probe-results page
 * is already loaded, no extra request.
 */
function HTTPDetail({ id, monitor }: { id: string; monitor: Monitor }) {
  const [window, setWindow] = useState<LatencyWindow>("24h");
  const latencyQuery = useMonitorLatency(id, window);
  const probesQuery = useMonitorProbeResults(id, { limit: PROBE_PAGE_SIZE });

  const probes: ProbeResult[] = probesQuery.data?.pages.flatMap((p) => p.results) ?? [];
  const failedProbes = probes.filter((p) => !p.ok);
  const tlsExpiresAt = latestTLSExpiry(probes);

  return (
    <>
      <MonitorHTTPStatRow monitor={monitor} latencyPoints={latencyQuery.data?.points ?? []} />

      <div className="mb-[22px]">
        <UptimeBar dailyStats={monitor.daily_stats} />
      </div>

      <TLSExpiryNote expiresAt={tlsExpiresAt} />

      <div className="mt-3 mb-[22px]">
        <h2 className="mb-3 text-sm font-medium text-text">Latency</h2>
        {latencyQuery.isLoading ? (
          <LatencyChartLoading />
        ) : (
          <LatencyChart
            points={latencyQuery.data?.points ?? []}
            failedProbes={failedProbes}
            window={window}
            onWindowChange={setWindow}
          />
        )}
      </div>

      <HTTPConfigSummary monitor={monitor} className="mb-[22px]" />

      <div className="mb-[22px]">
        <h2 className="mb-3 text-sm font-medium text-text">Probe log</h2>
        {probesQuery.isLoading ? (
          <ProbeLogLoading />
        ) : probesQuery.isError ? (
          <ProbeLogEmpty />
        ) : (
          <ProbeLog
            probes={probes}
            hasMore={probesQuery.hasNextPage}
            loadingMore={probesQuery.isFetchingNextPage}
            onLoadMore={() => probesQuery.fetchNextPage()}
          />
        )}
      </div>
    </>
  );
}

function HeartbeatDetail({ id, monitor }: { id: string; monitor: Monitor }) {
  const checkinsQuery = useMonitorCheckins(id, { limit: CHECKIN_PAGE_SIZE });
  const checkins = checkinsQuery.data?.pages.flatMap((p) => p.checkins) ?? [];

  return (
    <>
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
    </>
  );
}

export default function MonitorDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;

  const monitorQuery = useMonitor(id);
  const eventsQuery = useMonitorEvents(id, { limit: 20 });

  if (monitorQuery.isLoading) return <DetailLoading />;
  if (monitorQuery.isError) {
    const status = (monitorQuery.error as { status?: number } | undefined)?.status;
    if (status === 404 || status === 403) return <NotFound />;
    return <DetailError onRetry={() => monitorQuery.refetch()} />;
  }

  const monitor = monitorQuery.data;
  if (!monitor) return <NotFound />;

  const events = eventsQuery.data?.events ?? [];

  return (
    <div>
      <MonitorDetailHeader monitor={monitor} />

      {monitor.kind === "http" ? (
        <HTTPDetail id={id} monitor={monitor} />
      ) : (
        <HeartbeatDetail id={id} monitor={monitor} />
      )}

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
