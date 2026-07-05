import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { MonitorForm } from "./monitor-form";
import type { CreateMonitorRequest, Monitor } from "@/types/monitor";

// lib/api.ts reads NEXT_PUBLIC_API_BASE_URL once at module load and falls
// back to "" (a same-origin relative fetch); MonitorForm is imported
// statically above, so lib/api.ts is already loaded before any per-test env
// var write would take effect. Match msw handlers on the relative path
// instead of setting the env var, so this doesn't depend on module load
// order the way hooks/use-monitors.test.tsx's per-test dynamic import does.
const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

beforeEach(() => {
  server.use(
    http.post("/api/v1/schedule/describe", () =>
      HttpResponse.json({ description: "every 5 minutes; alert if 5 min late" }),
    ),
  );
});

function renderForm(props: Partial<React.ComponentProps<typeof MonitorForm>> = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onSubmit = props.onSubmit ?? vi.fn(async () => {});
  render(
    <QueryClientProvider client={queryClient}>
      <MonitorForm
        submitLabel="Create monitor"
        submittingLabel="Creating…"
        {...props}
        onSubmit={onSubmit}
      />
    </QueryClientProvider>,
  );
  return { onSubmit };
}

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
    fail_streak: 0,
    alerts_muted: false,
    auto_resume: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("MonitorForm", () => {
  it("defaults to the heartbeat kind card with a live schedule preview", async () => {
    renderForm();

    expect(screen.getByRole("radio", { name: /Heartbeat/i })).toBeChecked();
    await waitFor(
      () =>
        expect(
          screen.getByText(
            (_, node) =>
              node?.tagName === "STRONG" &&
              node.textContent === "every 5 minutes; alert if 5 min late",
          ),
        ).toBeInTheDocument(),
      { timeout: 3000 },
    );
  });

  it("switching to HTTP check shows url/method fields with GET pre-selected and never resets to blank", async () => {
    const user = userEvent.setup();
    renderForm();

    await user.click(screen.getByText("HTTP check"));

    expect(await screen.findByLabelText("URL")).toBeInTheDocument();
    // Regression test: Radix's Select used to fire onValueChange("") during
    // its own mount sequence right as the http fields became visible,
    // silently wiping the "GET" default before the user ever touched the
    // control (see monitor-form.tsx's onSelectChange helper).
    expect(screen.getByLabelText("Method")).toHaveTextContent("GET");
  });

  it("shows the backend's inline field error on a 422 without a generic banner", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn(async () => {
      const { ApiError } = await import("@/lib/api");
      // Passes client-side zod validation (a well-formed https URL) so the
      // server's field error is what actually surfaces.
      throw new ApiError(422, "that hostname is not reachable", "url");
    });
    renderForm({ onSubmit });

    await user.click(screen.getByText("HTTP check"));
    await user.type(screen.getByLabelText("Name"), "web-health");
    await user.type(await screen.findByLabelText("URL"), "https://example.com");
    await user.click(screen.getByRole("button", { name: "Create monitor" }));

    expect(await screen.findByText("that hostname is not reachable")).toBeInTheDocument();
    expect(screen.queryByText("Unable to reach the server.")).not.toBeInTheDocument();
  });

  it("pre-fills advanced HTTP fields from an existing monitor", async () => {
    const user = userEvent.setup();
    const monitor = monitorFixture({
      fail_threshold: 3,
      http_config: { keyword: "ok", follow_redirects: false },
    });
    renderForm({ monitor, submitLabel: "Save changes", submittingLabel: "Saving…" });

    expect(screen.getByLabelText("Name")).toHaveValue("web-health");
    expect(screen.getByLabelText("URL")).toHaveValue("https://example.com/health");

    await user.click(screen.getByText("Advanced", { exact: true }));
    expect(screen.getByLabelText("Confirmation threshold")).toHaveValue(3);
    expect(screen.getByLabelText("Body must contain")).toHaveValue("ok");
  });

  it("submits the full CreateMonitorRequest shape built from form values", async () => {
    const user = userEvent.setup();
    let captured: CreateMonitorRequest | undefined;
    const onSubmit = vi.fn(async (body: CreateMonitorRequest) => {
      captured = body;
    });
    renderForm({ onSubmit });

    await user.type(screen.getByLabelText("Name"), "nightly-backup");
    await user.click(screen.getByRole("button", { name: "Create monitor" }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(captured?.kind).toBe("heartbeat");
    expect(captured?.name).toBe("nightly-backup");
    expect(captured?.schedule_kind).toBe("period");
  });
});
