import { createServer } from "node:http";
import type { Server } from "node:http";
import { expect, test } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

// A real listening server (not page.route interception) — the backend, not
// the browser, makes the probe request, so the target has to be reachable
// from the API process. Loopback is SSRF-guarded by default
// (backend/worker/prober/probe.go); CI/local runs must set
// SSRF_ALLOWLIST=127.0.0.1/32,::1/128 (see e2e/README.md) or every probe here
// fails as "blocked" rather than exercising real up/down transitions.
class MockTarget {
  private server: Server;
  private healthy = true;
  port = 0;

  constructor() {
    this.server = createServer((_req, res) => {
      if (this.healthy) {
        res.writeHead(200).end("ok");
      } else {
        res.writeHead(503).end("unhealthy");
      }
    });
  }

  async start(): Promise<void> {
    await new Promise<void>((resolve) => this.server.listen(0, "127.0.0.1", resolve));
    const addr = this.server.address();
    if (addr && typeof addr === "object") this.port = addr.port;
  }

  setHealthy(v: boolean): void {
    this.healthy = v;
  }

  get url(): string {
    return `http://127.0.0.1:${this.port}/health`;
  }

  async stop(): Promise<void> {
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}

test.describe.serial("HTTP monitor lifecycle", () => {
  let page: Page;
  let accessToken: string;
  let target: MockTarget;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    target = new MockTarget();
    await target.start();

    const email = `http-lifecycle-e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`;
    await page.goto("/register");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill("correcthorsebatterystaple");

    const [registerResponse] = await Promise.all([
      page.waitForResponse(
        (res) => res.url().endsWith("/api/v1/auth/register") && res.request().method() === "POST",
      ),
      page.getByRole("button", { name: "Create account" }).click(),
    ]);
    await expect(page).toHaveURL(/\/dashboard$/);

    const body = (await registerResponse.json()) as { access_token: string };
    accessToken = body.access_token;
  });

  test.afterAll(async () => {
    await page.close();
    await target.stop();
  });

  async function advanceClock(request: APIRequestContext, seconds: number): Promise<void> {
    const res = await request.post(`${API_BASE_URL}/test/advance-clock`, {
      data: { seconds },
    });
    if (!res.ok()) {
      throw new Error(`advance-clock failed: ${res.status()} ${await res.text()}`);
    }
  }

  test("probe fail (default fail_threshold=2) -> down -> recover", async ({ request }) => {
    const createRes = await request.post(`${API_BASE_URL}/api/v1/monitors`, {
      headers: { Authorization: `Bearer ${accessToken}` },
      data: {
        kind: "http",
        name: "lifecycle-http-monitor",
        url: target.url,
        method: "GET",
        interval_s: 30,
        timeout_s: 10,
      },
    });
    expect(createRes.ok()).toBe(true);
    const monitor = (await createRes.json()) as { id: string };

    // Initial probe happens at creation-adjacent next_probe_at; advance once
    // to let the first (passing) probe land, confirming a healthy baseline.
    // MonitorDetailHeader (components/app/monitor-detail-header.tsx) renders
    // the state word ("UP"/"DOWN") right beside the h1; asserting on it avoids
    // ambiguity with the per-event StatusChip rendered in the feed below.
    await advanceClock(request, 30);
    await page.goto(`/monitors/${monitor.id}`);
    await expect(page.getByText("UP", { exact: true })).toBeVisible();

    target.setHealthy(false);

    // fail_threshold defaults to 2 (backend/worker/prober/prober.go): the
    // monitor needs two consecutive failed probes, each requiring its own
    // advance past next_probe_at, to flip to down.
    await advanceClock(request, 30);
    await advanceClock(request, 30);

    await page.reload();
    await expect(page.getByText("DOWN", { exact: true })).toBeVisible();

    target.setHealthy(true);
    await advanceClock(request, 30);

    await page.reload();
    await expect(page.getByText("UP", { exact: true })).toBeVisible();
  });
});
