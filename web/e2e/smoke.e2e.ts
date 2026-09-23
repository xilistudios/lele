/**
 * HARNESS SMOKE TEST — not a product test.
 *
 * Validates that the Playwright harness runs and pins the DOM selector contract that
 * the real WebUI message-ordering spec will depend on. The real ordering spec arrives
 * in a follow-up task; this test deliberately touches neither the network nor the real
 * app (it renders a fixture via `page.setContent`).
 *
 * The fixture mirrors the DOM contract produced by the app:
 * - `src/components/organisms/MessageList.tsx` → wrapper `data-testid="message-list"`
 * - `src/components/organisms/MessageBubble.tsx` → bubble roots carrying
 *   `data-testid="message"`, `data-role={message.role}` and
 *   `data-message-id={message.stableId ?? message.id}` (three return sites).
 *
 * NOTE: the real list is virtualized with Virtuoso, so only visible bubbles exist in
 * the DOM — the future ordering spec must account for that. This static fixture keeps
 * exactly three bubbles so every assertion is total.
 */
import { expect, test } from '@playwright/test'

test('harness smoke: message-list fixture honors the selector contract', async ({ page }) => {
  await page.setContent(`
    <div data-testid="message-list">
      <div data-testid="message" data-role="user" data-message-id="m1">first question</div>
      <div data-testid="message" data-role="assistant" data-message-id="m2">first answer</div>
      <div data-testid="message" data-role="user" data-message-id="m3">second question</div>
    </div>
  `)

  // The list wrapper is visible.
  await expect(page.getByTestId('message-list')).toBeVisible()

  // All three bubbles are present.
  const messages = page.getByTestId('message')
  await expect(messages).toHaveCount(3)

  // data-role reads back in document order: user, assistant, user.
  const roles = await messages.evaluateAll((els) => els.map((el) => el.getAttribute('data-role')))
  expect(roles).toEqual(['user', 'assistant', 'user'])

  // data-message-id: three pairwise-distinct values.
  const ids = await messages.evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-message-id')),
  )
  expect(new Set(ids).size).toBe(3)
})
