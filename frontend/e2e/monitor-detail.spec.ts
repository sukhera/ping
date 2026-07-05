import { expect, test } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

// Auth endpoints are rate-limited per IP (5/min each for /register and
// /login — backend/server/auth.go), and the existing suite (auth-flow.spec.ts
// + dashboard.spec.ts) already uses the full register budget with no margin.
// So this file registers exactly ONCE for all three tests below: they run
// serially (test.describe.serial) sharing one `page`/session created in
// beforeAll, instead of each getting its own fresh page (which would need
// its own login to get a session, adding 3 more auth calls the suite's
// budget doesn't have room for).
test.describe.serial("monitor detail", () => {
  let page: Page;
  let accessToken: string;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();

    const email = `detail-e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`;
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

  async function createHeartbeatMonitor(
    request: APIRequestContext,
    name: string,
  ): Promise<{ id: string; slug: string; ping_url: string }> {
    const res = await request.post(`${API_BASE_URL}/api/v1/monitors`, {
      headers: { Authorization: `Bearer ${accessToken}` },
      data: {
        kind: "heartbeat",
        name,
        schedule_kind: "period",
        period_s: 300,
        tz: "UTC",
        grace_s: 60,
      },
    });
    if (!res.ok()) {
      throw new Error(`create monitor failed: ${res.status()} ${await res.text()}`);
    }
    return res.json();
  }

  test("check-in body with HTML/script content renders inert on the detail page", async ({ request }) => {
    const monitor = await createHeartbeatMonitor(request, "xss-checkin-monitor");

    const xssBody = '<script>window.__xss_ran = true;</script><img src=x onerror="window.__xss_ran = true">';
    const pingRes = await request.post(monitor.ping_url, { data: xssBody });
    expect(pingRes.ok()).toBe(true);

    await page.goto(`/monitors/${monitor.id}`);
    await expect(page.getByRole("heading", { name: "xss-checkin-monitor" })).toBeVisible();

    // The raw markup must be visible as literal text in the check-in log...
    await expect(page.getByText(xssBody, { exact: false })).toBeVisible();
    // ...and must never have executed.
    const xssRan = await page.evaluate(() => (window as unknown as { __xss_ran?: boolean }).__xss_ran);
    expect(xssRan).toBeUndefined();
    expect(await page.locator('script:has-text("window.__xss_ran")').count()).toBe(0);
  });

  test("How to ping copy buttons produce a working command with the real slug URL", async ({ request }) => {
    const monitor = await createHeartbeatMonitor(request, "copy-button-monitor");

    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto(`/monitors/${monitor.id}`);

    await page.getByRole("button", { name: "Copy curl command" }).click();
    const copiedCurl = await page.evaluate(() => navigator.clipboard.readText());
    expect(copiedCurl).toBe(`curl -fsS ${monitor.ping_url}`);

    const pingRes = await request.post(monitor.ping_url);
    expect(pingRes.ok()).toBe(true);
  });

  test("pause action is optimistic: status chip updates before the request resolves", async ({ request }) => {
    const monitor = await createHeartbeatMonitor(request, "optimistic-pause-monitor");

    await page.goto(`/monitors/${monitor.id}`);

    // Delay the pause response so the optimistic update is observable before
    // the network round-trip completes.
    await page.route(`**/api/v1/monitors/${monitor.id}/pause`, async (route) => {
      await new Promise((r) => setTimeout(r, 500));
      await route.continue();
    });

    await page.getByRole("button", { name: "Pause" }).click();
    await expect(page.getByRole("img", { name: "status: paused" })).toBeVisible({ timeout: 300 });
  });
});
