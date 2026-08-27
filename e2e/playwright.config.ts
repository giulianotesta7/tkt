import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  globalSetup: "./global-setup.ts",
  globalTeardown: "./global-teardown.ts",
  reporter: [
    ["list"],
    ["html", { outputFolder: "../playwright-report" }],
    ["json", { outputFile: "../playwright-report/results.json" }],
  ],
  use: {
    // Lazy getter: evaluated at access time, AFTER globalSetup has set
    // process.env.TKT_BASE_URL to the dynamically allocated server port.
    get baseURL(): string {
      return process.env.TKT_BASE_URL ?? "http://127.0.0.1:8080";
    },
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
  outputDir: "../test-results",
});