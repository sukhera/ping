import { expect, test } from "@playwright/test";
import type { APIRequestContext, Page } from "@playwright/test";

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

test.describe.serial("monitor create/edit form", () => {
  let page: Page;
  let accessToken: string;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();

    const email = `monitor-form-e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`;
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

  async function createHttpMonitor(
    request: APIRequestContext,
    name: string,
  ): Promise<{ id: string }> {
    const res = await request.post(`${API_BASE_URL}/api/v1/monitors`, {
      headers: { Authorization: `Bearer ${accessToken}` },
      data: {
        kind: "http",
        name,
        url: "https://example.com/health",
        method: "GET",
        interval_s: 60,
        timeout_s: 10,
        fail_threshold: 2,
      },
    });
    if (!res.ok()) {
      throw new Error(`create monitor failed: ${res.status()} ${await res.text()}`);
    }
    return res.json();
  }

  test("a heartbeat monitor can be created end-to-end using only the keyboard", async () => {
    await page.goto("/monitors/new");
    await expect(page.getByRole("heading", { name: "New monitor" })).toBeVisible();

    // Name is autofocused on mount (heartbeat/period/grace all have sane
    // defaults already), so the shortest keyboard path is: type the name,
    // then Tab forward to the submit button.
    await expect(page.getByLabel("Name")).toBeFocused();
    await page.keyboard.type("keyboard-only-monitor");

    const [createResponse] = await Promise.all([
      page.waitForResponse(
        (res) => res.url().endsWith("/api/v1/monitors") && res.request().method() === "POST",
      ),
      (async () => {
        for (let i = 0; i < 10; i++) {
          const focused = await page.evaluate(() => document.activeElement?.textContent);
          if (focused === "Create monitor") break;
          await page.keyboard.press("Tab");
        }
        await page.keyboard.press("Enter");
      })(),
    ]);
    expect(createResponse.ok()).toBe(true);

    await expect(page).toHaveURL(/\/monitors\/[0-9a-f-]+$/);
    await expect(page.getByRole("heading", { name: "keyboard-only-monitor" })).toBeVisible();
  });

  test("the heartbeat/http kind cards are reachable and selectable via keyboard alone", async () => {
    await page.goto("/monitors/new");
    await expect(page.getByLabel("Name")).toBeFocused();

    // Shift+Tab backward from Name reaches the kind-card radio group; Right
    // arrow moves the native radio selection from heartbeat to http.
    await page.keyboard.press("Shift+Tab");
    await expect(page.getByRole("radio", { name: /Heartbeat/i })).toBeFocused();
    await page.keyboard.press("ArrowRight");
    await expect(page.getByRole("radio", { name: /HTTP check/i })).toBeChecked();
    await expect(page.getByRole("textbox", { name: "URL" })).toBeVisible();
  });

  test("editing an HTTP monitor pre-fills every field including advanced, and an unchanged save is a no-op", async () => {
    const monitor = await createHttpMonitor(page.request, "edit-prefill-monitor");

    await page.goto(`/monitors/${monitor.id}/edit`);
    await expect(page.getByRole("heading", { name: "Edit edit-prefill-monitor" })).toBeVisible();

    await expect(page.getByLabel("Name")).toHaveValue("edit-prefill-monitor");
    await expect(page.getByRole("textbox", { name: "URL" })).toHaveValue(
      "https://example.com/health",
    );
    await expect(page.getByLabel("Method")).toHaveText("GET");
    await expect(page.getByLabel("Check every (seconds)")).toHaveValue("60");
    await expect(page.getByLabel("Timeout (seconds)")).toHaveValue("10");

    await page.getByText("Advanced", { exact: true }).click();
    await expect(page.getByLabel("Confirmation threshold")).toHaveValue("2");

    const [saveResponse] = await Promise.all([
      page.waitForResponse(
        (res) =>
          res.url().endsWith(`/api/v1/monitors/${monitor.id}`) &&
          res.request().method() === "PATCH",
      ),
      page.getByRole("button", { name: "Save changes" }).click(),
    ]);
    expect(saveResponse.ok()).toBe(true);
    await expect(page).toHaveURL(`/monitors/${monitor.id}`);

    // An unchanged submit must not produce a config_change event (PING-015 AC).
    await expect(page.getByText("Configuration updated")).toHaveCount(0);
  });

  test("an invalid cron expression shows the API's field error inline", async () => {
    await page.goto("/monitors/new");
    await page.getByLabel("Name").fill("bad-cron-monitor");

    await page.getByRole("combobox", { name: "Schedule" }).click();
    await page.getByRole("option", { name: "Cron expression" }).click();
    await page.getByLabel("Cron expression").fill("not a cron");
    await page.getByLabel("Cron expression").blur();

    await expect(page.getByText(/expected a 5-field cron expression/i)).toBeVisible();
  });
});
