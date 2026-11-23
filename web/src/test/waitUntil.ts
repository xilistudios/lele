import { act } from '@testing-library/react'

export async function waitUntil(
  predicate: () => boolean,
  what = 'condition',
  timeoutMs = 8000,
): Promise<void> {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  throw new Error(`waitUntil: ${what} never became true`)
}
