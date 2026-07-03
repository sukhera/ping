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
});
