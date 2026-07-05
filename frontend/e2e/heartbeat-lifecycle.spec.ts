import { expect, test } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

// The full PRD N6 heartbeat critical path: register -> create -> ping -> up ->
// time-warp past grace -> down + alert event -> ping -> recovered. Requires
// the backend built with `-tags e2e` and PING_ENV=test (see e2e/README.md) —
// /test/advance-clock does not exist otherwise (TestAdvanceClock_AbsentWithoutE2ETag,
// backend/server/testclock_notag_test.go).
test.describe.serial("heartbeat monitor lifecycle", () => {
  let page: Page;
  let accessToken: string;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();

    const email = `heartbeat-lifecycle-e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`;
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
  });

  async function advanceClock(request: APIRequestContext, seconds: number): Promise<void> {
    const res = await request.post(`${API_BASE_URL}/test/advance-clock`, {
      data: { seconds },
    });
    if (!res.ok()) {
      throw new Error(`advance-clock failed: ${res.status()} ${await res.text()}`);
    }
  }

  test("up -> down + alert event -> recovered across a real grace deadline", async ({ request }) => {
    // Minimums enforced by schedule.MinPeriod/MinGrace (backend/schedule/errors.go):
    // both are 1 minute, so a single 125s advance crosses period(60) + grace(60).
    const createRes = await request.post(`${API_BASE_URL}/api/v1/monitors`, {
      headers: { Authorization: `Bearer ${accessToken}` },
      data: {
        kind: "heartbeat",
        name: "lifecycle-heartbeat",
        schedule_kind: "period",
        period_s: 60,
        tz: "UTC",
        grace_s: 60,
      },
    });
    expect(createRes.ok()).toBe(true);
    const monitor = (await createRes.json()) as { id: string; ping_url: string };

    const firstPing = await request.post(monitor.ping_url);
    expect(firstPing.ok()).toBe(true);

    // MonitorDetailHeader (components/app/monitor-detail-header.tsx) renders
    // the state word ("UP"/"DOWN") right beside the h1; asserting on it avoids
    // ambiguity with the per-event StatusChip rendered in the feed below.
    await page.goto(`/monitors/${monitor.id}`);
    await expect(page.getByText("UP", { exact: true })).toBeVisible();

    await advanceClock(request, 125);

    await page.reload();
    await expect(page.getByText("DOWN", { exact: true })).toBeVisible();

    const eventsRes = await request.get(`${API_BASE_URL}/api/v1/monitors/${monitor.id}/events`, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    expect(eventsRes.ok()).toBe(true);
    const { events } = (await eventsRes.json()) as { events: Array<{ type: string }> };
    expect(events.filter((e) => e.type === "down")).toHaveLength(1);

    const secondPing = await request.post(monitor.ping_url);
    expect(secondPing.ok()).toBe(true);

    await page.reload();
    await expect(page.getByText("UP", { exact: true })).toBeVisible();
  });
});
