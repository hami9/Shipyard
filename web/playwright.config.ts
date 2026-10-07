import { defineConfig, devices } from "@playwright/test";

// The UI's end-to-end tests (P6.6): real API and database, Chromium only
// (the owner's choice, 2026-10-07). See e2e/setup.mjs.
export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/setup.mjs",
  // The tests share one seeded database and change it: one at a time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env["CI"],
  retries: 0,
  timeout: 30_000,
  reporter: process.env["CI"] ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    trace: "retain-on-failure",
    // Colors and contrast are checked in both schemes (accessibility.spec.ts).
    colorScheme: "light",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
