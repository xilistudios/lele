import { expect, test } from '@playwright/test'

test('harness smoke: message-list fixture honors the selector contract', async ({ page }) => {
  await page.setContent(`
    <div data-testid="message-list">
      <div data-testid="message" data-role="user" data-message-id="m1">first question</div>
      <div data-testid="message" data-role="assistant" data-message-id="m2">first answer</div>
      <div data-testid="message" data-role="user" data-message-id="m3">second question</div>
    </div>
  `)

  await expect(page.getByTestId('message-list')).toBeVisible()

  const messages = page.getByTestId('message')
  await expect(messages).toHaveCount(3)

  const roles = await messages.evaluateAll((els) => els.map((el) => el.getAttribute('data-role')))
  expect(roles).toEqual(['user', 'assistant', 'user'])

  const ids = await messages.evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-message-id')),
  )
  expect(new Set(ids).size).toBe(3)
})
