import '../../test/setup'
import { describe, expect, test } from 'bun:test'
import { render } from '@testing-library/react'
import { PlusCircleIcon } from './Icons'

/**
 * Regression test for the "New chat" icon rendering a solid pink disc
 * instead of a pink disc with a visible white plus glyph.
 *
 * Root cause: design tokens in index.css are channel triplets
 * (e.g. `255 255 255`), not valid CSS colours. They must be consumed as
 * `rgb(var(--token))`, not bare `var(--token)`. A bare var() produces the
 * string "255 255 255" which is an invalid CSS colour, so the SVG stroke
 * silently falls back to `none` and the plus glyph disappears.
 */

describe('PlusCircleIcon', () => {
  /** The plus glyph must keep a visible stroke. */
  test('plus glyph keeps a visible stroke', () => {
    const { container } = render(<PlusCircleIcon />)
    // The <path> that draws the "+" — it's the second child of the <svg>.
    const svg = container.querySelector('svg')
    const path = svg?.querySelector('path')
    expect(path).not.toBeNull()

    const stroke = path?.getAttribute('stroke')
    expect(stroke).toBeTruthy()
    expect(stroke?.length).toBeGreaterThan(0)
  })

  /** The stroke token must be wrapped in rgb() so channel triplets become a valid colour. */
  test('stroke token is wrapped in rgb() not bare var()', () => {
    const { container } = render(<PlusCircleIcon />)
    const svg = container.querySelector('svg')
    const path = svg?.querySelector('path')
    const stroke = path?.getAttribute('stroke')

    // Must match the rgb(var(--color-...)) form used by the Tailwind config.
    expect(stroke).toMatch(/^rgb\(var\(--color-[a-z0-9-]+\)\)$/)

    // Explicit anti-regression: must NOT be the bare var() form.
    expect(stroke).not.toBe('var(--color-text-on-accent)')
  })
})
