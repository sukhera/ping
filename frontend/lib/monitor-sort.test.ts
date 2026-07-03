import { describe, expect, it } from "vitest";

import { sortMonitors } from "./monitor-sort";
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

describe("sortMonitors", () => {
  it("orders down -> late -> new -> up -> paused", () => {
    const shuffled = [
      monitor({ name: "e-paused", display_state: "paused" }),
      monitor({ name: "c-new", display_state: "new" }),
      monitor({ name: "a-down", display_state: "down" }),
      monitor({ name: "d-up", display_state: "up" }),
      monitor({ name: "b-late", display_state: "late" }),
    ];

    const sorted = sortMonitors(shuffled).map((m) => m.display_state);

    expect(sorted).toEqual(["down", "late", "new", "up", "paused"]);
  });

  it("breaks ties within the same state alphabetically by name", () => {
    const monitors = [
      monitor({ name: "zebra", display_state: "up" }),
      monitor({ name: "apple", display_state: "up" }),
      monitor({ name: "mango", display_state: "up" }),
    ];

    const sorted = sortMonitors(monitors).map((m) => m.name);

    expect(sorted).toEqual(["apple", "mango", "zebra"]);
  });

  it("does not mutate the input array", () => {
    const monitors = [monitor({ name: "b", display_state: "up" }), monitor({ name: "a", display_state: "down" })];
    const original = [...monitors];

    sortMonitors(monitors);

    expect(monitors).toEqual(original);
  });
});
