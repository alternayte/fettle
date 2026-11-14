import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./specs",
  globalSetup: "./setup.ts",
  globalTeardown: "./teardown.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  retries: 0,
  reporter: [["list"], ["json", { outputFile: process.env.FETTLE_REPORT_DIR ? `${process.env.FETTLE_REPORT_DIR}/playwright.json` : "test-results/playwright.json" }]],
  use: { baseURL: "http://127.0.0.1:7480", trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
