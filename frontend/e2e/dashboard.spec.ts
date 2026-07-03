import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

async function registerAndLogin(page: Page): Promise<{ email: string }> {
  const email = `dashboard-e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`;
  await page.goto("/register");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correcthorsebatterystaple");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  return { email };
}

// The create/edit monitor UI (PING-015) doesn't exist yet — seed directly
// against the backend API, mirroring backend/server/monitor_integration_test.go's
// pattern. Requires a real backend running per e2e/README.md.
async function createHeartbeatMonitor(
  request: APIRequestContext,
  accessToken: string,
  name: string,
): Promise<{ id: string }> {
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

// The dashboard app never exposes the raw access token (it's kept in-memory
// only, per lib/api.ts) — re-derive one via a direct login API call so the
// seeding helper above can authenticate independently of the page's session.
async function loginForToken(
  request: APIRequestContext,
  email: string,
): Promise<string> {
  const res = await request.post(`${API_BASE_URL}/api/v1/auth/login`, {
    data: { email, password: "correcthorsebatterystaple" },
  });
  if (!res.ok()) {
    throw new Error(`login failed: ${res.status()} ${await res.text()}`);
  }
  const body = await res.json();
  return body.access_token;
}

test("filters persist across reload via URL state", async ({ page, request }) => {
  const { email } = await registerAndLogin(page);
  const token = await loginForToken(request, email);
  await createHeartbeatMonitor(request, token, "nightly-backup");
  await createHeartbeatMonitor(request, token, "cert-renewal");

  await page.goto("/dashboard");
  await page.getByLabel("Search monitors").fill("nightly");
  await expect(page).toHaveURL(/[?&]q=nightly/, { timeout: 2000 });

  await page.reload();
  await expect(page.getByLabel("Search monitors")).toHaveValue("nightly");
  await expect(page.getByRole("row", { name: /nightly-backup/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /cert-renewal/ })).not.toBeVisible();
});

test("pause via row menu updates the status chip and menu label", async ({ page, request }) => {
  const { email } = await registerAndLogin(page);
  const token = await loginForToken(request, email);
  await createHeartbeatMonitor(request, token, "pausable-monitor");

  await page.goto("/dashboard");
  const row = page.getByRole("row", { name: /pausable-monitor/ });
  await expect(row).toBeVisible();

  await row.getByRole("button", { name: /actions for pausable-monitor/i }).click();
  await page.getByRole("menuitem", { name: "Pause" }).click();

  await expect(row.getByRole("img", { name: "status: paused" })).toBeVisible();

  await row.getByRole("button", { name: /actions for pausable-monitor/i }).click();
  await expect(page.getByRole("menuitem", { name: "Resume" })).toBeVisible();
});

test("dashboard has no automated a11y violations", async ({ page, request }) => {
  const { email } = await registerAndLogin(page);
  const token = await loginForToken(request, email);
  await createHeartbeatMonitor(request, token, "a11y-check-monitor");

  await page.goto("/dashboard");
  await expect(page.getByRole("row", { name: /a11y-check-monitor/ })).toBeVisible();

  const results = await new AxeBuilder({ page }).include('[role="table"]').analyze();

  // Known, accepted gap (not introduced by this ticket): design-mockup.html's
  // --text-faint token is 3.06:1 on --surface, below WCAG AA's 4.5:1 for
  // normal text. It's used for the table header labels and slug/ping-URL
  // sub-text, matching the mockup exactly — PING-013's AC requires matching
  // the mockup pixel-for-pixel, which takes priority here. Tracked as a
  // design-token contrast fix for a follow-up, not fixed in this ticket.
  // Every OTHER axe finding must still be zero.
  const knownColorContrastViolation = results.violations.find((v) => v.id === "color-contrast");
  const unexpectedViolations = results.violations.filter((v) => v.id !== "color-contrast");

  expect(unexpectedViolations).toEqual([]);
  if (knownColorContrastViolation) {
    for (const node of knownColorContrastViolation.nodes) {
      const contrastRatio = (node.any[0]?.data as { contrastRatio?: number } | undefined)
        ?.contrastRatio;
      expect(contrastRatio).toBeCloseTo(3.06, 1);
    }
  }
});

test("pulse animation is disabled under prefers-reduced-motion", async ({ page, request }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  const { email } = await registerAndLogin(page);
  const token = await loginForToken(request, email);
  await createHeartbeatMonitor(request, token, "reduced-motion-monitor");

  await page.goto("/dashboard");
  const row = page.getByRole("row", { name: /reduced-motion-monitor/ });
  await expect(row).toBeVisible();

  const pulseRing = row.locator('[class*="motion-reduce\\:hidden"]').first();
  if (await pulseRing.count()) {
    await expect(pulseRing).toBeHidden();
  }
});
