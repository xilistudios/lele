import { expect } from 'bun:test'

export const SELECTED_BACKGROUND_CLASS = 'bg-surface-selected'
export const ACCESSIBLE_ACCENT_CLASS = 'text-accent-text'
export const INADEQUATE_ACCENT_CLASS = 'text-accent-primary'

export function classTokens(element: Element | null): string[] {
  if (!element) throw new Error('selected control not found in the rendered tree')
  return (element.getAttribute('class') ?? '').split(/\s+/).filter(Boolean)
}

export function expectSelectedAccentRecipe(element: Element | null): void {
  const tokens = classTokens(element)
  expect(tokens).toContain(SELECTED_BACKGROUND_CLASS)
  expect(tokens).toContain(ACCESSIBLE_ACCENT_CLASS)
  expect(tokens).not.toContain(INADEQUATE_ACCENT_CLASS)
}
