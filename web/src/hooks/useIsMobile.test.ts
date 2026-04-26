import { afterEach, describe, expect, test } from 'bun:test'
import { renderHook } from '@testing-library/react'
import { useIsMobile } from './useIsMobile'

// Mock window.matchMedia
const originalMatchMedia = globalThis.matchMedia
const originalInnerWidth = window.innerWidth

describe('useIsMobile', () => {
  afterEach(() => {
    globalThis.matchMedia = originalMatchMedia
    Object.defineProperty(window, 'innerWidth', { value: originalInnerWidth, configurable: true })
  })

  test('detects mobile viewport (< 768px)', () => {
    Object.defineProperty(window, 'innerWidth', { value: 500, configurable: true })
    globalThis.matchMedia = (() => ({
      matches: true,
      media: '',
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof globalThis.matchMedia

    const { result } = renderHook(() => useIsMobile(768))
    expect(result.current).toBe(true)
  })

  test('detects desktop viewport (> 768px)', () => {
    Object.defineProperty(window, 'innerWidth', { value: 1024, configurable: true })
    globalThis.matchMedia = (() => ({
      matches: false,
      media: '',
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof globalThis.matchMedia

    const { result } = renderHook(() => useIsMobile(768))
    expect(result.current).toBe(false)
  })

  test('uses provided breakpoint', () => {
    Object.defineProperty(window, 'innerWidth', { value: 500, configurable: true })
    globalThis.matchMedia = ((query: string) => ({
      matches: query === '(max-width: 1023px)',
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof globalThis.matchMedia

    const { result } = renderHook(() => useIsMobile(1024))
    expect(result.current).toBe(true)
  })
})
