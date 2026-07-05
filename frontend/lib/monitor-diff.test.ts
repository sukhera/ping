import { describe, expect, it } from "vitest";

import { diffMonitorUpdate } from "./monitor-diff";
import type { Monitor, UpdateMonitorRequest } from "@/types/monitor";

function monitorFixture(overrides: Partial<Monitor> = {}): Monitor {
  return {
    id: "m1",
    kind: "http",
    slug: "abc123",
    name: "web-health",
    state: "up",
    display_state: "up",
    url: "https://example.com/health",
    method: "GET",
    interval_s: 60,
    timeout_s: 10,
    fail_threshold: 2,
    http_config: {},
    fail_streak: 0,
    alerts_muted: false,
    auto_resume: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("diffMonitorUpdate", () => {
  it("produces an empty diff when the form resubmits unchanged scalar fields", () => {
    const monitor = monitorFixture();
    const body: UpdateMonitorRequest = {
      name: "web-health",
      url: "https://example.com/health",
      method: "GET",
      interval_s: 60,
      timeout_s: 10,
      fail_threshold: 2,
      auto_resume: true,
    };

    expect(diffMonitorUpdate(monitor, body)).toEqual({});
  });

  it("produces an empty diff for http_config when the server's {} is compared against the form's spelled-out defaults", () => {
    // A monitor created without ever setting http_config round-trips as {}
    // (not undefined) from the backend — see backend response
    // `"http_config":{}`. The edit form always reconstructs a fully
    // populated object (keyword_negate: false, follow_redirects: true, ...)
    // from its own field defaults, so a literal/JSON.stringify comparison
    // of {} against that reconstructed object would wrongly report a
    // change on a completely untouched edit (this was a real bug caught by
    // CI, not locally, because the local run's fixtures always set
    // http_config explicitly).
    const monitor = monitorFixture({ http_config: {} });
    const body: UpdateMonitorRequest = {
      name: "web-health",
      url: "https://example.com/health",
      method: "GET",
      interval_s: 60,
      timeout_s: 10,
      fail_threshold: 2,
      auto_resume: true,
      http_config: {
        headers: undefined,
        keyword: undefined,
        keyword_negate: false,
        follow_redirects: true,
      },
    };

    expect(diffMonitorUpdate(monitor, body)).toEqual({});
  });

  it("includes http_config in the diff when the keyword actually changes", () => {
    const monitor = monitorFixture({ http_config: { keyword: "ok" } });
    const body: UpdateMonitorRequest = {
      http_config: {
        headers: undefined,
        keyword: "healthy",
        keyword_negate: false,
        follow_redirects: true,
      },
    };

    const diff = diffMonitorUpdate(monitor, body);
    expect(diff.http_config?.keyword).toBe("healthy");
  });

  it("includes http_config in the diff when headers actually change", () => {
    const monitor = monitorFixture({ http_config: { headers: { Authorization: "Bearer old" } } });
    const body: UpdateMonitorRequest = {
      http_config: {
        headers: { Authorization: "Bearer new" },
        keyword: undefined,
        keyword_negate: false,
        follow_redirects: true,
      },
    };

    const diff = diffMonitorUpdate(monitor, body);
    expect(diff.http_config?.headers).toEqual({ Authorization: "Bearer new" });
  });

  it("never leaks a kind key into the diff even though MonitorForm always builds a CreateMonitorRequest-shaped body", () => {
    const monitor = monitorFixture();
    const body = {
      kind: "http",
      name: "web-health",
      url: "https://example.com/health",
      method: "GET",
      interval_s: 60,
      timeout_s: 10,
      fail_threshold: 2,
      auto_resume: true,
    } as UpdateMonitorRequest;

    const diff = diffMonitorUpdate(monitor, body);
    expect(diff).not.toHaveProperty("kind");
    expect(diff).toEqual({});
  });

  it("diffs a renamed field on an otherwise-unchanged heartbeat monitor", () => {
    const monitor = monitorFixture({
      kind: "heartbeat",
      name: "nightly-backup",
      schedule_kind: "period",
      period_s: 300,
      tz: "UTC",
      grace_s: 300,
      url: undefined,
      method: undefined,
      interval_s: undefined,
      timeout_s: undefined,
      fail_threshold: undefined,
      http_config: undefined,
    });
    const body: UpdateMonitorRequest = {
      name: "nightly-backup-renamed",
      schedule_kind: "period",
      period_s: 300,
      tz: "UTC",
      grace_s: 300,
      auto_resume: true,
    };

    expect(diffMonitorUpdate(monitor, body)).toEqual({ name: "nightly-backup-renamed" });
  });
});
