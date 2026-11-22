import '../../test/setup'
import { describe, expect, test } from 'bun:test'
import { render } from '@testing-library/react'
import { PlusCircleIcon } from './Icons'

describe('PlusCircleIcon', () => {
  test('plus glyph keeps a visible stroke', () => {
    const { container } = render(<PlusCircleIcon />)
    const svg = container.querySelector('svg')
    const path = svg?.querySelector('path')
    expect(path).not.toBeNull()

    const stroke = path?.getAttribute('stroke')
    expect(stroke).toBeTruthy()
    expect(stroke?.length).toBeGreaterThan(0)
  })

  test('stroke token is wrapped in rgb() not bare var()', () => {
    const { container } = render(<PlusCircleIcon />)
    const svg = container.querySelector('svg')
    const path = svg?.querySelector('path')
    const stroke = path?.getAttribute('stroke')

    expect(stroke).toMatch(/^rgb\(var\(--color-[a-z0-9-]+\)\)$/)

    expect(stroke).not.toBe('var(--color-text-on-accent)')
  })
})
