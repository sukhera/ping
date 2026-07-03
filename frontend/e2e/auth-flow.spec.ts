import { expect, test } from "@playwright/test";

test("register → dashboard (empty) → theme toggle → logout", async ({
  page,
}) => {
  const email = `playwright-test-${Date.now()}@example.com`;

  await page.goto("/register");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill("correcthorsebatterystaple");
  await page.getByRole("button", { name: "Create account" }).click();

  await expect(page).toHaveURL(/\/dashboard$/);

  const html = page.locator("html");
  await expect(html).toHaveClass(/dark/);

  await page.getByRole("button", { name: "Change theme" }).click();
  await page.getByText("Light").click();
  await expect(html).toHaveClass(/light/);

  await page.getByRole("button", { name: `Account menu for ${email}` }).click();
  await page.getByText("Log out").click();

  await expect(page).toHaveURL(/\/login$/);
});
