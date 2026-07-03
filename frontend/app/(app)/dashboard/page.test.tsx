import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const BASE = "http://localhost:8080";
const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

beforeEach(() => {
  process.env.NEXT_PUBLIC_API_BASE_URL = BASE;
});

let currentSearchParams = new URLSearchParams();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/dashboard",
  useSearchParams: () => currentSearchParams,
}));

// lib/api.ts reads NEXT_PUBLIC_API_BASE_URL at module-load time — reset
// modules per test (same as use-monitors.test.tsx) so an earlier test file's
// import doesn't leave a stale BASE_URL baked into this page's fetch calls.
async function freshDashboardPage() {
  vi.resetModules();
  const mod = await import("./page");
  return mod.default;
}

async function renderPage() {
  const DashboardPage = await freshDashboardPage();
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <DashboardPage />
    </QueryClientProvider>,
  );
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

describe("DashboardPage", () => {
  beforeEach(() => {
    currentSearchParams = new URLSearchParams();
  });

  it("shows the empty state when there are no monitors", async () => {
    server.use(http.get(`${BASE}/api/v1/monitors`, () => HttpResponse.json({ monitors: [] })));

    await renderPage();

    expect(await screen.findByText("No monitors yet.")).toBeInTheDocument();
  });

  it("shows an error state with a retry action on fetch failure", async () => {
    server.use(
      http.get(`${BASE}/api/v1/monitors`, () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );

    await renderPage();

    expect(await screen.findByText(/couldn.t load monitors/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });

  it("renders monitor rows sorted problem-first regardless of API order", async () => {
    server.use(
      http.get(`${BASE}/api/v1/monitors`, () =>
        HttpResponse.json({
          monitors: [
            monitorFixture({ id: "up1", name: "z-up", display_state: "up" }),
            monitorFixture({ id: "down1", name: "a-down", display_state: "down" }),
            monitorFixture({ id: "late1", name: "m-late", display_state: "late" }),
          ],
        }),
      ),
    );

    await renderPage();

    // rows[0] is the header row; wait for all 3 monitor rows to have rendered.
    await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(4));
    const rows = screen.getAllByRole("row");
    expect(rows[1]).toHaveTextContent("a-down");
    expect(rows[2]).toHaveTextContent("m-late");
    expect(rows[3]).toHaveTextContent("z-up");
  });

  it("shows the stat strip counts computed from the monitor list", async () => {
    server.use(
      http.get(`${BASE}/api/v1/monitors`, () =>
        HttpResponse.json({
          monitors: [
            monitorFixture({ id: "up1", name: "up-one", display_state: "up" }),
            monitorFixture({ id: "up2", name: "up-two", display_state: "up" }),
            monitorFixture({ id: "down1", name: "down-one", display_state: "down" }),
          ],
        }),
      ),
    );

    await renderPage();

    const statSection = await screen.findByRole("region", { name: "Status summary" });
    await waitFor(() => expect(within(statSection).getAllByText("2")).toHaveLength(1));
    expect(within(statSection).getByText("Up")).toBeInTheDocument();
    expect(within(statSection).getByText("Down")).toBeInTheDocument();
    expect(within(statSection).getByText("1")).toBeInTheDocument();
  });
});
