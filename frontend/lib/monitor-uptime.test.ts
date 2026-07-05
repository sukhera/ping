import { describe, expect, it } from "vitest";

import { computeUptimeWindows } from "./monitor-uptime";
import type { DailyStat } from "@/types/monitor";

function dayKey(offsetFromToday: number): string {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - offsetFromToday);
  return d.toISOString().slice(0, 10);
}

describe("computeUptimeWindows", () => {
  it("returns null for every window when there is no data", () => {
    expect(computeUptimeWindows([])).toEqual({ d7: null, d30: null, d90: null });
  });

  it("computes each window from only the days within it", () => {
    const stats: DailyStat[] = [
      { day: dayKey(0), checkins: 10, failures: 0, downtime_s: 0 },
      { day: dayKey(6), checkins: 10, failures: 0, downtime_s: 0 }, // last day in the 7d window
      { day: dayKey(10), checkins: 10, failures: 10, downtime_s: 600 }, // outside 7d, inside 30d
      { day: dayKey(89), checkins: 10, failures: 0, downtime_s: 0 }, // last day in the 90d window
    ];

    const windows = computeUptimeWindows(stats);
    expect(windows.d7).toBe(100); // only the two clean days within 7d
    expect(windows.d30).toBeCloseTo((20 / 30) * 100, 5); // clean 20 + failed 10 of 30
    expect(windows.d90).toBeCloseTo((30 / 40) * 100, 5); // all four days now in range
  });

  it("matches monitor-row.tsx's overall uptime% formula for a single window", () => {
    const stats: DailyStat[] = [{ day: dayKey(0), checkins: 8, failures: 2, downtime_s: 30 }];
    const windows = computeUptimeWindows(stats);
    expect(windows.d7).toBeCloseTo(75, 5);
  });
});
