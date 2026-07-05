import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { LatencyChart } from "./latency-chart";
import type { LatencyPoint, ProbeResult } from "@/types/monitor";

function point(overrides: Partial<LatencyPoint> = {}): LatencyPoint {
  return {
    bucket_start: new Date().toISOString(),
    p50: 100,
    p95: 200,
    avg: 120,
    sample_count: 10,
    ...overrides,
  };
}

describe("LatencyChart", () => {
  it("renders the empty state with zero data points", () => {
    render(
      <LatencyChart points={[]} failedProbes={[]} window="24h" onWindowChange={() => {}} />,
    );
    expect(screen.getByText("No latency data yet.")).toBeInTheDocument();
  });

  it("renders with a single (sparse) data point without crashing", () => {
    render(
      <LatencyChart points={[point()]} failedProbes={[]} window="24h" onWindowChange={() => {}} />,
    );
    expect(screen.getByRole("img", { name: /latency chart/i })).toBeInTheDocument();
  });

  it("marks the active window toggle and calls onWindowChange on click", async () => {
    const user = userEvent.setup();
    const onWindowChange = vi.fn();
    render(
      <LatencyChart
        points={[point()]}
        failedProbes={[]}
        window="24h"
        onWindowChange={onWindowChange}
      />,
    );

    const btn24h = screen.getByRole("button", { name: "24h" });
    const btn7d = screen.getByRole("button", { name: "7d" });
    expect(btn24h).toHaveAttribute("aria-pressed", "true");
    expect(btn7d).toHaveAttribute("aria-pressed", "false");

    await user.click(btn7d);
    expect(onWindowChange).toHaveBeenCalledWith("7d");
  });

  it("includes failure count in the chart's accessible name", () => {
    const failed: ProbeResult[] = [
      {
        id: 1,
        monitor_id: "m1",
        ok: false,
        error: "timeout",
        created_at: new Date().toISOString(),
      },
    ];
    render(
      <LatencyChart points={[point()]} failedProbes={failed} window="24h" onWindowChange={() => {}} />,
    );
    expect(screen.getByRole("img", { name: /1 failures/ })).toBeInTheDocument();
  });

  it("counts a failure inside the still-filling final bucket, not just past bucket starts", () => {
    // Regression: points are bucket *starts*, so a failure timestamped after
    // the last point's bucket_start (but still inside that bucket's window)
    // must not be dropped by a domainEnd that stops exactly at the last
    // point's own timestamp.
    const now = Date.now();
    const bucketMs = 5 * 60 * 1000;
    const points = [
      point({ bucket_start: new Date(now - bucketMs).toISOString() }),
      point({ bucket_start: new Date(now).toISOString() }),
    ];
    const failed: ProbeResult[] = [
      {
        id: 1,
        monitor_id: "m1",
        ok: false,
        error: "dns lookup failed",
        created_at: new Date(now + bucketMs / 2).toISOString(),
      },
    ];
    render(
      <LatencyChart points={points} failedProbes={failed} window="24h" onWindowChange={() => {}} />,
    );
    expect(screen.getByRole("img", { name: /1 failures/ })).toBeInTheDocument();
  });
});
