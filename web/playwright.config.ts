import { defineConfig, devices } from "@playwright/test";

// The browser suite (docs/design/testing.md, steps 4 to 6, and the axe
// checks of docs/specs/web.md 7). The global setup starts a Go fixture
// server per client mode. Chrome runs with client certificates on. WebKit
// runs with them off: on macOS, Playwright's WebKit presents client
// certificates only to the special host local.playwright, whose origin the
// server would refuse; real Safari with certificates is part of the manual
// run on three machines.
export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/setup.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env["CI"],
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env["CI"] ? [["list"], ["github"]] : "list",
  use: {
    ignoreHTTPSErrors: true, // the fixture's CA is not in the system store
    locale: "en-US",
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chrome", use: { ...devices["Desktop Chrome"], channel: "chrome" }, metadata: { clientCerts: "on" } },
    { name: "webkit", use: { ...devices["Desktop Safari"] }, metadata: { clientCerts: "off" } },
  ],
});
