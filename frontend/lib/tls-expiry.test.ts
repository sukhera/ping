import { describe, expect, it } from "vitest";

import { daysUntil, latestTLSExpiry } from "./tls-expiry";
import type { ProbeResult } from "@/types/monitor";

function probe(overrides: Partial<ProbeResult> = {}): ProbeResult {
  return {
    id: 1,
    monitor_id: "m1",
    ok: true,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("latestTLSExpiry", () => {
  it("returns the newest probe's tls_expires_at when present", () => {
    const probes = [
      probe({ id: 3, tls_expires_at: "2026-08-01T00:00:00Z" }),
      probe({ id: 2, tls_expires_at: "2026-07-30T00:00:00Z" }),
    ];
    expect(latestTLSExpiry(probes)).toBe("2026-08-01T00:00:00Z");
  });

  it("walks forward past probes without TLS data (e.g. a failed probe)", () => {
    const probes = [
      probe({ id: 3, ok: false, error: "timeout" }),
      probe({ id: 2, tls_expires_at: "2026-07-30T00:00:00Z" }),
    ];
    expect(latestTLSExpiry(probes)).toBe("2026-07-30T00:00:00Z");
  });

  it("returns undefined when no probe has TLS data", () => {
    expect(latestTLSExpiry([probe(), probe({ id: 2 })])).toBeUndefined();
  });

  it("returns undefined for an empty probe list", () => {
    expect(latestTLSExpiry([])).toBeUndefined();
  });
});

describe("daysUntil", () => {
  it("computes whole days remaining until a future date", () => {
    const now = new Date("2026-07-05T00:00:00Z");
    expect(daysUntil("2026-08-15T00:00:00Z", now)).toBe(41);
  });

  it("returns a negative number for a date already in the past", () => {
    const now = new Date("2026-07-05T00:00:00Z");
    expect(daysUntil("2026-06-01T00:00:00Z", now)).toBeLessThan(0);
  });
});
