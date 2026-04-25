import { describe, expect, it, mock, test } from 'bun:test'
import { renderHook } from '@testing-library/react'
import { useIsMobile } from './useIsMobile'

// Mock window.matchMedia
const originalMatchMedia = globalThis.matchMedia

describe('useIsMobile', () => {
  afterEach(() => {
    globalThis.matchMedia = originalMatchMedia
  })

  test('detecta viewport mobile (< 768px)', () => {
    globalThis.matchMedia = (() => ({
      matches: true,
      media: '',
      onchange: null,
      addEventListener: (_type: string, cb: (e: MediaQueryListEvent) => void) => {},
      removeEventListener: (_type: string, cb: (e: MediaQueryListEvent) => void) => {},
      dispatchEvent: () => false,
    })) as unknown as typeof globalThis.matchMedia

    const { result } = renderHook(() => useIsMobile(768))
    expect(result.current).toBe(true)
  })

  test('detecta viewport desktop (> 768px)', () => {
    globalThis.matchMedia = (() => ({
      matches: false,
      media: '',
      onchange: null,
      addEventListener: (_type: string, cb: (e: MediaQueryListEvent) => void) => {},
      removeEventListener: (_type: string, cb: (e: MediaQueryListEvent) => void) => {},
      dispatchEvent: () => false,
    })) as unknown as typeof globalThis.matchMedia

    const { result } = renderHook(() => useIsMobile(768))
    expect(result.current).toBe(false)
  })

  test('usa el breakpoint proporcionado', () => {
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
