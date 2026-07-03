import { describe, expect, it } from "vitest";

import { formatRelativeTime } from "./format-relative-time";

const NOW = new Date("2026-07-03T12:00:00Z");

describe("formatRelativeTime", () => {
  it("formats sub-minute durations in seconds", () => {
    expect(formatRelativeTime(new Date("2026-07-03T11:59:54Z"), NOW)).toBe("6s");
  });

  it("formats sub-hour durations in minutes", () => {
    expect(formatRelativeTime(new Date("2026-07-03T11:31:00Z"), NOW)).toBe("29m");
  });

  it("formats hour-scale durations as hours and minutes", () => {
    expect(formatRelativeTime(new Date("2026-07-03T09:48:00Z"), NOW)).toBe("2h 12m");
  });

  it("omits zero minutes on an exact hour boundary", () => {
    expect(formatRelativeTime(new Date("2026-07-03T10:00:00Z"), NOW)).toBe("2h");
  });

  it("formats day-scale durations as days and hours", () => {
    expect(formatRelativeTime(new Date("2026-07-01T09:00:00Z"), NOW)).toBe("2d 3h");
  });

  it("clamps a future date to 0s rather than a negative duration", () => {
    expect(formatRelativeTime(new Date("2026-07-03T12:05:00Z"), NOW)).toBe("0s");
  });

  it("accepts an ISO string directly", () => {
    expect(formatRelativeTime("2026-07-03T11:59:54Z", NOW)).toBe("6s");
  });
});
