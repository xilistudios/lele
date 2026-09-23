/**
 * Browser-instrumentation harness for the WebUI message-ordering regression work.
 *
 * This config intentionally defines NO `webServer`: this slice needs no dev server.
 * The real message-ordering spec (plus any server wiring) lands in a follow-up task.
 */
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  // WHY this distinct suffix: Bun's test runner also collects `*.spec.ts` files, so
  // Playwright specs must use a distinct suffix (`*.e2e.ts`); otherwise `bun test`
  // picks them up and aborts with "Playwright Test did not expect test() to be
  // called here". Keeping `.e2e.ts` here makes the two runners' file sets disjoint.
  testMatch: '**/*.e2e.ts',
  // Build the ordering harness bundle (e2e/harness) before any test runs.
  globalSetup: './e2e/global-setup.ts',
  outputDir: 'test-results/e2e',
  reporter: [['list']],

  // Order-sensitive regression work: deterministic, one browser at a time.
  fullyParallel: false,
  retries: 0,
  workers: 1,
  timeout: 30_000,

  use: {
    headless: true,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
