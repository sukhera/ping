import { describe, expect, it } from "vitest";

import { computeStats } from "./monitor-stats";
import type { Monitor } from "@/types/monitor";

function monitor(overrides: Partial<Monitor>): Monitor {
  return {
    id: overrides.name ?? "m",
    kind: "heartbeat",
    slug: "slug",
    name: "monitor",
    state: "up",
    display_state: "up",
    fail_streak: 0,
    alerts_muted: false,
    auto_resume: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("computeStats", () => {
  it("returns zero counts and null uptime for an empty list", () => {
    expect(computeStats([])).toEqual({ up: 0, down: 0, late: 0, uptimePct: null, downSample: undefined });
  });

  it("counts up/down/late, excluding paused and new from those buckets", () => {
    const monitors = [
      monitor({ name: "a", display_state: "up" }),
      monitor({ name: "b", display_state: "up" }),
      monitor({ name: "c", display_state: "down" }),
      monitor({ name: "d", display_state: "late" }),
      monitor({ name: "e", display_state: "paused" }),
      monitor({ name: "f", display_state: "new" }),
    ];

    const stats = computeStats(monitors);

    expect(stats.up).toBe(2);
    expect(stats.down).toBe(1);
    expect(stats.late).toBe(1);
  });

  it("reports uptimePct as null when no monitor has any daily_stats data", () => {
    const monitors = [monitor({ name: "a", display_state: "up", daily_stats: [] })];

    expect(computeStats(monitors).uptimePct).toBeNull();
  });

  it("computes uptimePct across all monitors' daily_stats when present", () => {
    const monitors = [
      monitor({
        name: "a",
        display_state: "up",
        daily_stats: [{ day: "2026-07-01", checkins: 100, failures: 2, downtime_s: 0 }],
      }),
      monitor({
        name: "b",
        display_state: "up",
        daily_stats: [{ day: "2026-07-01", checkins: 100, failures: 0, downtime_s: 0 }],
      }),
    ];

    // (200 - 2) / 200 = 99%
    expect(computeStats(monitors).uptimePct).toBeCloseTo(99, 5);
  });

  it("surfaces the first down monitor's name and relative time as downSample", () => {
    const monitors = [
      monitor({ name: "first-down", display_state: "down", last_checkin_at: "2026-07-01T00:00:00Z" }),
      monitor({ name: "second-down", display_state: "down" }),
    ];

    const stats = computeStats(monitors);

    expect(stats.downSample?.name).toBe("first-down");
  });

  it("omits downSample when nothing is down", () => {
    const monitors = [monitor({ name: "a", display_state: "up" })];

    expect(computeStats(monitors).downSample).toBeUndefined();
  });
});
