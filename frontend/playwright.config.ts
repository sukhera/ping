import { defineConfig, devices } from "@playwright/test";

// This suite needs a real backend (Postgres + Redis + the Go API) in
// addition to the frontend dev server — Playwright's webServer option can
// only manage one process tree, so both are started manually. See
// e2e/README.md for the required setup before running `npm run e2e`.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  retries: 0,
  use: {
    baseURL: "http://localhost:3000",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
