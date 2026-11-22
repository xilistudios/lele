import '../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, renderHook } from '@testing-library/react'
import { type ReactNode, StrictMode, createElement } from 'react'
import { useAppViewportHeight } from './useAppViewportHeight'

/**
 * Contract of `useAppViewportHeight` (see the hook's doc comment).
 *
 * The hook owns exactly one side effect: an inline `--app-vh` on
 * `<html>` that exists ONLY while an on-screen keyboard shrinks the visual
 * viewport below the layout viewport. Everything else — no keyboard, pinch
 * zoom, unsupported browser, unmount — must leave the property absent so the
 * stylesheet's `dvh` value stands. Each case below is written so that the
 * mutation named in its comment turns it red.
 *
 * The property is process-wide (`document.documentElement` outlives the test),
 * so every case starts from a clean slate and `afterEach` removes it: a leak
 * would poison `App.test.tsx`, which renders `App` and therefore this hook.
 */

type Listener = () => void

type VvStub = {
  height: number
  scale: number
  addEventListener: (type: string, listener: Listener) => void
  removeEventListener: (type: string, listener: Listener) => void
}

const originalInnerHeight = Object.getOwnPropertyDescriptor(window, 'innerHeight')

let listeners: Record<string, Set<Listener>> = {}

function stubVisualViewport(height: number, scale = 1): VvStub {
  const vv: VvStub = {
    height,
    scale,
    addEventListener: (type, listener) => {
      listeners[type] ??= new Set()
      listeners[type].add(listener)
    },
    removeEventListener: (type, listener) => {
      listeners[type]?.delete(listener)
    },
  }
  Object.defineProperty(window, 'visualViewport', { value: vv, configurable: true })
  return vv
}

function stubInnerHeight(height: number): void {
  Object.defineProperty(window, 'innerHeight', { value: height, configurable: true })
}

function fire(type: string): void {
  for (const listener of [...(listeners[type] ?? [])]) listener()
}

function resizeVisualViewport(height: number, scale = 1): void {
  const vv = window.visualViewport as unknown as VvStub
  vv.height = height
  vv.scale = scale
  fire('resize')
}

function appVh(): string {
  return document.documentElement.style.getPropertyValue('--app-vh')
}

function listenerCount(type: string): number {
  return listeners[type]?.size ?? 0
}

afterEach(() => {
  cleanup()
  listeners = {}
  Reflect.deleteProperty(window, 'visualViewport')
  if (originalInnerHeight) {
    Object.defineProperty(window, 'innerHeight', originalInnerHeight)
  }
  document.documentElement.style.removeProperty('--app-vh')
})

describe('useAppViewportHeight', () => {
  test('without a visualViewport it renders and writes nothing', () => {
    stubInnerHeight(664)
    expect('visualViewport' in window).toBe(false)

    const { unmount } = renderHook(() => useAppViewportHeight())

    expect(appVh()).toBe('')
    expect(() => unmount()).not.toThrow()
    expect(appVh()).toBe('')
  })

  test('a visual viewport equal to the layout viewport leaves dvh in charge', () => {
    stubInnerHeight(664)
    stubVisualViewport(664, 1)

    renderHook(() => useAppViewportHeight())

    expect(appVh()).toBe('')
  })

  test('a keyboard-shrunk visual viewport pins the shell to the visible height', () => {
    stubInnerHeight(664)
    stubVisualViewport(364, 1)

    renderHook(() => useAppViewportHeight())

    expect(appVh()).toBe('364px')
  })

  test('closing the keyboard removes the px override (the dvh handoff)', () => {
    stubInnerHeight(664)
    stubVisualViewport(364, 1)

    renderHook(() => useAppViewportHeight())
    expect(appVh()).toBe('364px')

    resizeVisualViewport(664, 1)

    expect(appVh()).toBe('')
  })

  test('a pinch-zoomed viewport never shrinks the app', () => {
    stubInnerHeight(664)
    stubVisualViewport(300, 2.5)

    renderHook(() => useAppViewportHeight())

    expect(appVh()).toBe('')
  })

  test('zooming in while the keyboard is open clears the stale px value', () => {
    stubInnerHeight(664)
    stubVisualViewport(364, 1)

    renderHook(() => useAppViewportHeight())
    expect(appVh()).toBe('364px')

    resizeVisualViewport(364, 2.5)

    expect(appVh()).toBe('')
  })

  test('unmount removes every listener and the property', () => {
    stubInnerHeight(664)
    stubVisualViewport(364, 1)

    const { unmount } = renderHook(() => useAppViewportHeight())
    expect(appVh()).toBe('364px')
    expect(listenerCount('resize')).toBe(1)
    expect(listenerCount('scroll')).toBe(1)

    unmount()

    expect(listenerCount('resize')).toBe(0)
    expect(listenerCount('scroll')).toBe(0)
    expect(appVh()).toBe('')

    fire('resize')
    expect(appVh()).toBe('')
  })

  test('a StrictMode double mount ends with one listener set and the right value', () => {
    stubInnerHeight(664)
    stubVisualViewport(364, 1)

    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(StrictMode, null, children)
    const { unmount } = renderHook(() => useAppViewportHeight(), { wrapper })

    expect(listenerCount('resize')).toBe(1)
    expect(listenerCount('scroll')).toBe(1)
    expect(appVh()).toBe('364px')

    unmount()

    expect(listenerCount('resize')).toBe(0)
    expect(listenerCount('scroll')).toBe(0)
    expect(appVh()).toBe('')
  })
})
