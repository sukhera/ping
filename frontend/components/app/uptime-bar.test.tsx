import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { UptimeBar } from "./uptime-bar";
import type { DailyStat } from "@/types/monitor";

function dayKey(offsetFromToday: number): string {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - offsetFromToday);
  return d.toISOString().slice(0, 10);
}

describe("UptimeBar", () => {
  it("renders `days` cells, all no-data (--border) when dailyStats is empty", () => {
    const { container } = render(<UptimeBar dailyStats={[]} days={7} />);

    const rects = container.querySelectorAll("rect");
    expect(rects).toHaveLength(7);
    for (const rect of Array.from(rects)) {
      expect(rect.getAttribute("fill")).toBe("var(--border)");
    }
  });

  it("classifies a zero-failure day as clean (--up at 60% opacity)", () => {
    const stats: DailyStat[] = [{ day: dayKey(0), checkins: 10, failures: 0, downtime_s: 0 }];
    const { container } = render(<UptimeBar dailyStats={stats} days={1} />);

    const rect = container.querySelector("rect")!;
    expect(rect.getAttribute("fill")).toBe("var(--up)");
    expect(rect.getAttribute("fill-opacity")).toBe("0.6");
  });

  it("classifies failures with no downtime as degraded (--late)", () => {
    const stats: DailyStat[] = [{ day: dayKey(0), checkins: 10, failures: 2, downtime_s: 0 }];
    const { container } = render(<UptimeBar dailyStats={stats} days={1} />);

    const rect = container.querySelector("rect")!;
    expect(rect.getAttribute("fill")).toBe("var(--late)");
  });

  it("classifies any downtime as an incident (--down)", () => {
    const stats: DailyStat[] = [{ day: dayKey(0), checkins: 10, failures: 3, downtime_s: 600 }];
    const { container } = render(<UptimeBar dailyStats={stats} days={1} />);

    const rect = container.querySelector("rect")!;
    expect(rect.getAttribute("fill")).toBe("var(--down)");
  });

  it("treats a zero-checkins row as no-data, not clean", () => {
    const stats: DailyStat[] = [{ day: dayKey(0), checkins: 0, failures: 0, downtime_s: 0 }];
    const { container } = render(<UptimeBar dailyStats={stats} days={1} />);

    const rect = container.querySelector("rect")!;
    expect(rect.getAttribute("fill")).toBe("var(--border)");
  });

  it("gives each cell a native <title> tooltip", () => {
    const stats: DailyStat[] = [{ day: dayKey(0), checkins: 10, failures: 0, downtime_s: 0 }];
    const { container } = render(<UptimeBar dailyStats={stats} days={1} />);

    expect(container.querySelector("rect title")).not.toBeNull();
  });
});
