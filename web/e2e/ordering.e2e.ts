import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'

declare global {
  interface Window {
    __leleOrdering?: { scenarios: string[] }
  }
}

const harnessHtml = path.join(path.dirname(fileURLToPath(import.meta.url)), 'harness', 'index.html')

test('saturated-window: real mergeMessages renders the 50-message window in order', async ({
  page,
}) => {
  await page.goto(`file://${harnessHtml}`)

  const scenarioNames = await page.evaluate(() => window.__leleOrdering?.scenarios)
  expect(scenarioNames).toEqual(['saturated-window', 'reasoning-iteration'])

  const scenario = page.locator('[data-testid="scenario"][data-scenario="saturated-window"]')
  await expect(scenario).toHaveCount(1)

  const bubbles = scenario.getByTestId('message')

  await expect(bubbles).toHaveCount(50)

  const roles = await bubbles.evaluateAll((els) => els.map((el) => el.getAttribute('data-role')))
  const expectedRoles = Array.from({ length: 50 }, (_, i) => (i % 2 === 0 ? 'user' : 'assistant'))
  expect(roles).toEqual(expectedRoles)

  const texts = await scenario
    .getByTestId('message-text')
    .evaluateAll((els) => els.map((el) => el.textContent))
  const expectedTexts: string[] = []
  for (let n = 4; n <= 28; n++) {
    expectedTexts.push(`question number ${n}`, `answer number ${n}`)
  }
  expect(texts).toEqual(expectedTexts)

  const lastText = await bubbles.last().getByTestId('message-text').textContent()
  expect(lastText).toBe('answer number 28')

  const ids = await bubbles.evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-message-id')),
  )
  expect(new Set(ids).size).toBe(50)

  expect(new Set(texts).size).toBe(texts.length)
})
test('reasoning-iteration: a text-less thinking step is not stranded after newer answers', async ({
  page,
}) => {
  await page.goto(`file://${harnessHtml}`)

  const scenario = page.locator('[data-testid="scenario"][data-scenario="reasoning-iteration"]')
  await expect(scenario).toHaveCount(1)

  const bubbles = scenario.getByTestId('message')

  await expect(bubbles).toHaveCount(52)

  const thinking = scenario.locator('[data-testid="message"][data-has-thinking="true"]')
  await expect(thinking).toHaveCount(1)

  const tail = await scenario
    .getByTestId('message')
    .evaluateAll((els) =>
      els.slice(-4).map((el) => `${el.getAttribute('data-has-thinking')}:${el.textContent}`),
    )
  expect(tail).toEqual([
    'false:question number 25',
    'true:',
    'false:answer number 25',
    'false:question number 26',
  ])

  const lastRole = await bubbles.last().getAttribute('data-role')
  expect(lastRole).toBe('user')

  const ids = await bubbles.evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-message-id')),
  )
  expect(new Set(ids).size).toBe(52)
})
