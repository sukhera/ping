import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const BASE = "http://localhost:8080";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

beforeEach(() => {
  process.env.NEXT_PUBLIC_API_BASE_URL = BASE;
});

// use-monitors.ts pulls in lib/api.ts, which keeps module-level state (the
// access token) — reset modules per test the same way lib/api.test.ts does
// so tests can't leak state into each other.
async function freshHooks() {
  vi.resetModules();
  const mod = await import("./use-monitors");
  return mod;
}

function wrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function monitorFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: "m1",
    kind: "heartbeat",
    slug: "abc123",
    name: "nightly backup",
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

describe("useMonitors", () => {
  it("serializes q/kind/state/cursor/limit params into the querystring", async () => {
    let capturedUrl: string | undefined;
    server.use(
      http.get(`${BASE}/api/v1/monitors`, ({ request }) => {
        capturedUrl = request.url;
        return HttpResponse.json({ monitors: [] });
      }),
    );
    const { useMonitors } = await freshHooks();

    const { result } = renderHook(
      () => useMonitors({ q: "backup", kind: "heartbeat", state: "up", cursor: "abc", limit: 10 }),
      { wrapper: wrapper() },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const url = new URL(capturedUrl!);
    expect(url.searchParams.get("q")).toBe("backup");
    expect(url.searchParams.get("kind")).toBe("heartbeat");
    expect(url.searchParams.get("state")).toBe("up");
    expect(url.searchParams.get("cursor")).toBe("abc");
    expect(url.searchParams.get("limit")).toBe("10");
  });

  it("omits unset params entirely", async () => {
    let capturedUrl: string | undefined;
    server.use(
      http.get(`${BASE}/api/v1/monitors`, ({ request }) => {
        capturedUrl = request.url;
        return HttpResponse.json({ monitors: [] });
      }),
    );
    const { useMonitors } = await freshHooks();

    const { result } = renderHook(() => useMonitors({}), { wrapper: wrapper() });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(capturedUrl).toBe(`${BASE}/api/v1/monitors`);
  });
});

describe("usePauseMonitor", () => {
  it("writes the mutation response into the cached list query", async () => {
    const initial = monitorFixture({ display_state: "up" });
    const paused = monitorFixture({ display_state: "paused", paused_at: "2026-01-02T00:00:00Z" });

    server.use(
      http.get(`${BASE}/api/v1/monitors`, () => HttpResponse.json({ monitors: [initial] })),
      http.post(`${BASE}/api/v1/monitors/m1/pause`, () => HttpResponse.json(paused)),
    );
    const { useMonitors, usePauseMonitor } = await freshHooks();
    const w = wrapper();

    const list = renderHook(() => useMonitors({}), { wrapper: w });
    await waitFor(() => expect(list.result.current.data?.monitors[0].display_state).toBe("up"));

    const mutation = renderHook(() => usePauseMonitor(), { wrapper: w });
    mutation.result.current.mutate("m1");

    await waitFor(() => expect(mutation.result.current.isSuccess).toBe(true));
    // The list query's cache entry should reflect the pause without a refetch.
    await waitFor(() =>
      expect(list.result.current.data?.monitors[0].display_state).toBe("paused"),
    );
  });

  it("patches the cache optimistically before the network response arrives", async () => {
    const initial = monitorFixture({ display_state: "up" });
    let resolvePause: (() => void) | undefined;
    const pauseGate = new Promise<void>((resolve) => {
      resolvePause = resolve;
    });

    server.use(
      http.get(`${BASE}/api/v1/monitors`, () => HttpResponse.json({ monitors: [initial] })),
      http.post(`${BASE}/api/v1/monitors/m1/pause`, async () => {
        await pauseGate;
        return HttpResponse.json(monitorFixture({ display_state: "paused", paused_at: "2026-01-02T00:00:00Z" }));
      }),
    );
    const { useMonitors, usePauseMonitor } = await freshHooks();
    const w = wrapper();

    const list = renderHook(() => useMonitors({}), { wrapper: w });
    await waitFor(() => expect(list.result.current.data?.monitors[0].display_state).toBe("up"));

    const mutation = renderHook(() => usePauseMonitor(), { wrapper: w });
    mutation.result.current.mutate("m1");

    // Cache reflects "paused" immediately, before the gated response resolves.
    await waitFor(() => expect(list.result.current.data?.monitors[0].display_state).toBe("paused"));
    expect(mutation.result.current.isSuccess).toBe(false);

    resolvePause?.();
    await waitFor(() => expect(mutation.result.current.isSuccess).toBe(true));
  });

  it("rolls back the optimistic patch when the mutation fails", async () => {
    const initial = monitorFixture({ display_state: "up" });
    let rejectPause: (() => void) | undefined;
    const pauseGate = new Promise<void>((_resolve, reject) => {
      rejectPause = () => reject(new Error("boom"));
    });

    server.use(
      http.get(`${BASE}/api/v1/monitors`, () => HttpResponse.json({ monitors: [initial] })),
      http.post(`${BASE}/api/v1/monitors/m1/pause`, async () => {
        await pauseGate.catch(() => {});
        return HttpResponse.json({ error: "boom" }, { status: 500 });
      }),
    );
    const { useMonitors, usePauseMonitor } = await freshHooks();
    const w = wrapper();

    const list = renderHook(() => useMonitors({}), { wrapper: w });
    await waitFor(() => expect(list.result.current.data?.monitors[0].display_state).toBe("up"));

    const mutation = renderHook(() => usePauseMonitor(), { wrapper: w });
    mutation.result.current.mutate("m1");

    // Optimistic patch applies immediately, before the gated response settles.
    await waitFor(() => expect(list.result.current.data?.monitors[0].display_state).toBe("paused"));
    expect(mutation.result.current.isError).toBe(false);

    rejectPause?.();

    // ...then rolls back once the mutation errors.
    await waitFor(() => expect(mutation.result.current.isError).toBe(true));
    await waitFor(() => expect(list.result.current.data?.monitors[0].display_state).toBe("up"));
  });
});

describe("useCreateMonitor", () => {
  it("seeds the detail cache and invalidates list queries on success", async () => {
    const created = monitorFixture({ id: "new-1", name: "new monitor" });
    server.use(
      http.get(`${BASE}/api/v1/monitors`, () => HttpResponse.json({ monitors: [] })),
      http.post(`${BASE}/api/v1/monitors`, () => HttpResponse.json(created, { status: 201 })),
    );
    const { useCreateMonitor, useMonitor } = await freshHooks();
    const w = wrapper();

    const mutation = renderHook(() => useCreateMonitor(), { wrapper: w });
    mutation.result.current.mutate({ kind: "heartbeat", name: "new monitor" });
    await waitFor(() => expect(mutation.result.current.isSuccess).toBe(true));

    const detail = renderHook(() => useMonitor("new-1"), { wrapper: w });
    await waitFor(() => expect(detail.result.current.data?.name).toBe("new monitor"));
  });
});

describe("useUpdateMonitor", () => {
  it("writes the server's returned monitor into the detail cache", async () => {
    server.use(
      http.get(`${BASE}/api/v1/monitors/m1`, () => HttpResponse.json(monitorFixture({ name: "old" }))),
      http.patch(`${BASE}/api/v1/monitors/m1`, () => HttpResponse.json(monitorFixture({ name: "renamed" }))),
    );
    const { useUpdateMonitor, useMonitor } = await freshHooks();
    const w = wrapper();

    const detail = renderHook(() => useMonitor("m1"), { wrapper: w });
    await waitFor(() => expect(detail.result.current.data?.name).toBe("old"));

    const mutation = renderHook(() => useUpdateMonitor("m1"), { wrapper: w });
    mutation.result.current.mutate({ name: "renamed" });
    await waitFor(() => expect(mutation.result.current.isSuccess).toBe(true));
    await waitFor(() => expect(detail.result.current.data?.name).toBe("renamed"));
  });
});

describe("useDescribeSchedule", () => {
  it("does not call the API while disabled", async () => {
    let called = false;
    server.use(
      http.post(`${BASE}/api/v1/schedule/describe`, () => {
        called = true;
        return HttpResponse.json({ description: "unused" });
      }),
    );
    const { useDescribeSchedule } = await freshHooks();

    renderHook(() => useDescribeSchedule({ schedule_kind: "period", period_s: 60 }, false), {
      wrapper: wrapper(),
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(called).toBe(false);
  });

  it("fetches the description and next_runs when enabled", async () => {
    server.use(
      http.post(`${BASE}/api/v1/schedule/describe`, () =>
        HttpResponse.json({
          description: "every day at 04:00 (UTC); alert if 30 min late",
          next_runs: ["2026-01-02T04:00:00Z"],
        }),
      ),
    );
    const { useDescribeSchedule } = await freshHooks();

    const { result } = renderHook(
      () =>
        useDescribeSchedule(
          { schedule_kind: "cron", cron_expr: "0 4 * * *", tz: "UTC", grace_s: 1800 },
          true,
        ),
      { wrapper: wrapper() },
    );

    await waitFor(() => expect(result.current.data?.description).toContain("every day at 04:00"));
    expect(result.current.data?.next_runs).toEqual(["2026-01-02T04:00:00Z"]);
  });
});
