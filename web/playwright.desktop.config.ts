/**
 * Desktop-height gate for the WebUI.
 *
 * The mobile gate pins phone widths; the base config is a no-server harness.
 * Neither covers the desktop layout on a SHORT viewport, where the settings
 * rail used to clip its bottom tabs (issue #444). This gate runs the real Farm
 * dev server with the heights the issue measured (1280×720 and 1366×768, where
 * the rail must scroll) plus 1440×900, where every tab fits and the rail must
 * stay inert, so the rail contract is exercised against real layout, not jsdom.
 *
 * Add a spec here whenever a desktop view has a height contract.
 */
import { defineConfig, devices } from '@playwright/test'

const BASE_URL = 'http://localhost:3005'

export default defineConfig({
  testDir: './e2e/desktop',
  testMatch: '**/*.e2e.ts',
  outputDir: 'test-results/e2e-desktop',
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
    {
      name: 'desktop-1280x720',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 720 } },
    },
    {
      name: 'desktop-1366x768',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1366, height: 768 } },
    },
    {
      name: 'desktop-1440x900',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } },
    },
  ],
})
