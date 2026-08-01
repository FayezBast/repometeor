import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 30_000,
  use: {
    baseURL: process.env.WEB_TEST_URL ?? "http://127.0.0.1:3000",
    trace: "retain-on-failure",
  },
  projects: [
    // channel: "chromium" runs the full Chromium build rather than the
    // separate chrome-headless-shell artifact, so `playwright install chromium`
    // is the only download the suite needs.
    {
      name: "desktop",
      use: { ...devices["Desktop Chrome"], channel: "chromium" },
    },
    // Pixel 5 is a Chromium mobile device. The mobile assertions here are about
    // viewport behaviour — layout, overflow, tap targets — which Chromium
    // exercises faithfully, so this avoids downloading a second browser engine
    // in CI for no additional signal. Add a WebKit project when a test needs
    // Safari-specific rendering.
    { name: "mobile", use: { ...devices["Pixel 5"], channel: "chromium" } },
  ],
  webServer: process.env.WEB_TEST_URL
    ? undefined
    : {
        command: "pnpm dev",
        url: "http://127.0.0.1:3000",
        reuseExistingServer: true,
      },
});
