"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useRef, useState } from "react";

import {
  MonitorBoard,
  MonitorBoardEmpty,
  MonitorBoardError,
  MonitorBoardLoading,
  MonitorBoardNoMatches,
} from "@/components/app/monitor-board";
import { MonitorFilters } from "@/components/app/monitor-filters";
import { StatBlock } from "@/components/app/stat-block";
import { Button } from "@/components/ui/button";
import { useMonitors } from "@/hooks/use-monitors";
import { computeStats } from "@/lib/monitor-stats";
import { sortMonitors } from "@/lib/monitor-sort";
import type { MonitorDisplayState, MonitorKind } from "@/types/monitor";

const SEARCH_DEBOUNCE_MS = 300;

/**
 * Owns the debounced search-box local state so typing feels instant while
 * the URL (and thus the actual query) updates on a delay. Mounted with
 * key={urlSearch} by the parent so external URL changes — clear-filters,
 * back/forward nav — reset this local state by remounting rather than a
 * setState-in-effect sync.
 */
function DebouncedSearch({
  initialValue,
  kind,
  state,
  onCommit,
  onKindChange,
  onStateChange,
}: {
  initialValue: string;
  kind: MonitorKind | null;
  state: MonitorDisplayState | null;
  onCommit: (value: string) => void;
  onKindChange: (value: MonitorKind | null) => void;
  onStateChange: (value: MonitorDisplayState | null) => void;
}) {
  const [value, setValue] = useState(initialValue);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  function handleChange(next: string) {
    setValue(next);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => onCommit(next), SEARCH_DEBOUNCE_MS);
  }

  return (
    <MonitorFilters
      search={value}
      kind={kind}
      state={state}
      onSearchChange={handleChange}
      onKindChange={onKindChange}
      onStateChange={onStateChange}
    />
  );
}

export default function DashboardPage() {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const urlSearch = searchParams.get("q") ?? "";
  const kind = (searchParams.get("kind") as MonitorKind | null) ?? null;
  const state = (searchParams.get("state") as MonitorDisplayState | null) ?? null;

  function updateParams(next: { q?: string | null; kind?: string | null; state?: string | null }, replace = false) {
    const params = new URLSearchParams(searchParams.toString());
    for (const [key, value] of Object.entries(next)) {
      if (value) params.set(key, value);
      else params.delete(key);
    }
    const qs = params.toString();
    const url = qs ? `${pathname}?${qs}` : pathname;
    if (replace) router.replace(url, { scroll: false });
    else router.push(url, { scroll: false });
  }

  const hasFilters = !!(urlSearch || kind || state);

  // Stat strip + sidebar always reflect the *unfiltered* set (matches the
  // mockup: global counts independent of the filter chips below them).
  const unfiltered = useMonitors({});
  const filtered = useMonitors(hasFilters ? { q: urlSearch, kind: kind ?? undefined, state: state ?? undefined } : {});

  const source = hasFilters ? filtered : unfiltered;
  const monitors = source.data?.monitors;
  const sorted = monitors ? sortMonitors(monitors) : undefined;

  const stats = computeStats(unfiltered.data?.monitors ?? []);

  function clearFilters() {
    router.push(pathname, { scroll: false });
  }

  return (
    <div>
      <div className="mb-[22px] flex items-center justify-between">
        <h1 className="text-xl font-semibold">Monitors</h1>
        <Button asChild>
          <Link href="/monitors/new">+ New monitor</Link>
        </Button>
      </div>

      <section aria-label="Status summary" className="mb-[22px] grid grid-cols-4 gap-3">
        <StatBlock label="Up" value={String(stats.up)} valueClassName="text-up" sub="all quiet" />
        <StatBlock
          label="Down"
          value={String(stats.down)}
          valueClassName="text-down"
          danger={stats.down > 0}
          sub={
            stats.downSample
              ? `${stats.downSample.name} · ${stats.downSample.since}`
              : "none"
          }
        />
        <StatBlock label="Late" value={String(stats.late)} valueClassName="text-late" sub="in grace period" />
        <StatBlock
          label="Uptime · 30d"
          value={stats.uptimePct !== null ? `${stats.uptimePct.toFixed(2)}%` : "—"}
          sub={stats.uptimePct !== null ? "across all monitors" : "no data yet"}
        />
      </section>

      <DebouncedSearch
        key={urlSearch}
        initialValue={urlSearch}
        kind={kind}
        state={state}
        onCommit={(value) => updateParams({ q: value || null }, true)}
        onKindChange={(v) => updateParams({ kind: v })}
        onStateChange={(v) => updateParams({ state: v })}
      />

      {source.isLoading ? (
        <MonitorBoardLoading />
      ) : source.isError ? (
        <MonitorBoardError onRetry={() => source.refetch()} />
      ) : sorted && sorted.length > 0 ? (
        <MonitorBoard monitors={sorted} />
      ) : hasFilters ? (
        <MonitorBoardNoMatches onClear={clearFilters} />
      ) : (
        <MonitorBoardEmpty />
      )}
    </div>
  );
}
