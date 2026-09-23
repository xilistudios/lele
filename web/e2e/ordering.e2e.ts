import path from 'node:path'
import { fileURLToPath } from 'node:url'
/**
 * BROWSER-LEVEL PIN — saturated-sliding-window message ordering.
 *
 * This is the real-Chromium pin for the reported bug where, once a conversation
 * exceeds 50 messages, the backend serves history as a sliding window of the
 * last 50 (pkg/channels/rest_chat.go), the cached non-optimistic user count
 * SATURATES, and an optimistic user bubble used to become immortal and get
 * stranded at the END of the list — after newer answers ("messages lose their
 * order until I refresh the page").
 *
 * It exercises the REAL `mergeMessages` from web/src/hooks/messageMerge.ts:
 * the harness bundle (e2e/harness/, built by e2e/global-setup.ts) imports it
 * directly — no reimplementation, no stub — runs it over the scenario data and
 * renders the merged output into a real DOM using the app's exact bubble DOM
 * contract (`data-testid="message-list"` / `data-testid="message"` /
 * `data-role` / `data-message-id = stableId ?? id`).
 *
 * The unit-level gate for the same scenario (R2) lives in
 * web/src/hooks/messageOrderingWindow.test.ts — this spec pins that the fix
 * actually reaches the rendered DOM order in a real browser.
 */
import { expect, test } from '@playwright/test'

declare global {
  interface Window {
    __leleOrdering?: { scenarios: string[] }
  }
}

// Resolved from the module location — no hardcoded machine path.
const harnessHtml = path.join(path.dirname(fileURLToPath(import.meta.url)), 'harness', 'index.html')

test('saturated-window: real mergeMessages renders the 50-message window in order', async ({
  page,
}) => {
  await page.goto(`file://${harnessHtml}`)

  // The harness actually booted (not a silently-blank page).
  const scenarioNames = await page.evaluate(() => window.__leleOrdering?.scenarios)
  expect(scenarioNames).toEqual(['saturated-window'])

  const scenario = page.locator('[data-testid="scenario"][data-scenario="saturated-window"]')
  await expect(scenario).toHaveCount(1)

  const bubbles = scenario.getByTestId('message')

  // 1) Exactly 50 bubbles: the three optimistic bubbles were confirmed and
  //    collapsed into their base copies — no duplicates, nothing stranded.
  await expect(bubbles).toHaveCount(50)

  // 2) data-role sequence reads back in document order: user, assistant × 25.
  const roles = await bubbles.evaluateAll((els) => els.map((el) => el.getAttribute('data-role')))
  const expectedRoles = Array.from({ length: 50 }, (_, i) => (i % 2 === 0 ? 'user' : 'assistant'))
  expect(roles).toEqual(expectedRoles)

  // 3) Exact text sequence: question/answer 4 … question/answer 28.
  const texts = await scenario
    .getByTestId('message-text')
    .evaluateAll((els) => els.map((el) => el.textContent))
  const expectedTexts: string[] = []
  for (let n = 4; n <= 28; n++) {
    expectedTexts.push(`question number ${n}`, `answer number ${n}`)
  }
  expect(texts).toEqual(expectedTexts)

  // 4) The LAST bubble is `answer number 28` — THE assertion that fails on the
  //    unfixed code, where three user bubbles were stranded after it.
  const lastText = await bubbles.last().getByTestId('message-text').textContent()
  expect(lastText).toBe('answer number 28')

  // 5) All 50 data-message-id values are pairwise distinct (the real React
  //    key contract: `stableId ?? id`).
  const ids = await bubbles.evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-message-id')),
  )
  expect(new Set(ids).size).toBe(50)

  // 6) No text appears twice.
  expect(new Set(texts).size).toBe(texts.length)
})
