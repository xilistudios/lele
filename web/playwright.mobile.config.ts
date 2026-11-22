/**
 * Mobile-viewport gate for the WebUI shell-height fix.
 *
 * Deliberately separate from playwright.config.ts: that one is a no-server
 * instrumentation harness (file:// + setContent), while this gate needs the
 * real Farm dev server and the real app shell. Specs live in e2e/mobile/ and
 * keep the `.e2e.ts` suffix so `bun test` never collects them, and the base
 * config ignores this spec directory so `bun run test:e2e` stays a harness run.
 */
import { defineConfig, devices } from '@playwright/test'

const BASE_URL = 'http://localhost:3005'

export default defineConfig({
  testDir: './e2e/mobile',
  testMatch: '**/*.e2e.ts',
  outputDir: 'test-results/e2e-mobile',
  reporter: [['list']],
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  use: { headless: true, baseURL: BASE_URL },
  webServer: {
    command: 'bun run dev',
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [
    { name: 'mobile-390', use: { ...devices['Pixel 5'], viewport: { width: 390, height: 664 } } },
    { name: 'mobile-360', use: { ...devices['Pixel 5'], viewport: { width: 360, height: 740 } } },
    { name: 'mobile-320', use: { ...devices['Pixel 5'], viewport: { width: 320, height: 568 } } },
  ],
})
