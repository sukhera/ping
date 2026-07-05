import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

test.describe.serial("API key CRUD", () => {
  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();

    const email = `api-keys-e2e-${Date.now()}-${Math.random().toString(36).slice(2)}@example.com`;
    await page.goto("/register");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password").fill("correcthorsebatterystaple");
    await page.getByRole("button", { name: "Create account" }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("create via UI, use as Bearer against the management API, revoke, then rejected", async ({
    request,
  }) => {
    await page.goto("/settings");
    await page.getByRole("tab", { name: "API keys" }).click();

    await page.getByLabel("Label").fill("ci runner");
    const [createResponse] = await Promise.all([
      page.waitForResponse(
        (res) => res.url().endsWith("/api/v1/apikeys") && res.request().method() === "POST",
      ),
      page.getByRole("button", { name: "New key" }).click(),
    ]);
    expect(createResponse.ok()).toBe(true);
    const { key } = (await createResponse.json()) as { key: string };
    expect(key).toMatch(/^pk_/);

    // The plaintext key is shown once in a dismissable banner, and the row
    // appears in the list below it.
    await expect(page.getByText("Copy this key now")).toBeVisible();
    await expect(page.getByText("ci runner")).toBeVisible();

    // Prove the key actually authenticates the scriptable management API
    // (PING-016's AC), independent of the web session.
    const listRes = await request.get(`${API_BASE_URL}/api/v1/monitors/`, {
      headers: { Authorization: `Bearer ${key}` },
    });
    expect(listRes.ok()).toBe(true);

    await page.getByRole("button", { name: "Done" }).click();
    await page.getByRole("button", { name: "Revoke" }).click();
    await page.getByRole("button", { name: "Revoke key" }).click();

    await expect(page.getByText("Revoked", { exact: true })).toBeVisible();

    const revokedRes = await request.get(`${API_BASE_URL}/api/v1/monitors/`, {
      headers: { Authorization: `Bearer ${key}` },
    });
    expect(revokedRes.status()).toBe(401);
  });
});
