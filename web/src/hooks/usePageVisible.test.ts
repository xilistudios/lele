/**
 * Tests for the page-visibility primitives used by the interval pollers.
 *
 * jsdom does not implement visibility changes: `document.visibilityState` is a
 * getter on `Document.prototype` and no `visibilitychange` event is ever
 * dispatched. Both are therefore simulated here — shadowing the getter with an
 * own data property (same trick as `services/ws/client.test.ts`) and firing the
 * event by hand — so the hook is exercised exactly the way a browser drives it.
 */

import { afterEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, renderHook } from '@testing-library/react'
import { isPageVisible, useOnPageVisible, usePageVisible } from './usePageVisible'

/** Set `document.visibilityState` and fire the event the browser would fire. */
function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

afterEach(() => {
  cleanup()
  // Reset the shadowed getter so a hidden state never leaks into another test.
  Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
})

describe('isPageVisible', () => {
  test('reflects document.visibilityState (visible / hidden)', () => {
    expect(isPageVisible()).toBe(true)

    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    expect(isPageVisible()).toBe(false)

    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    expect(isPageVisible()).toBe(true)
  })

  test('is SSR safe: no document means "visible" instead of throwing', () => {
    const doc = globalThis.document
    // Simulating the server: the global is simply absent.
    Reflect.deleteProperty(globalThis, 'document')
    try {
      expect(isPageVisible()).toBe(true)
    } finally {
      globalThis.document = doc
    }
  })
})

describe('usePageVisible', () => {
  test('starts from the current visibility state', () => {
    const { result } = renderHook(() => usePageVisible())
    expect(result.current).toBe(true)
  })

  test('a hook mounted while hidden starts hidden', () => {
    setVisibility('hidden')

    const { result } = renderHook(() => usePageVisible())
    expect(result.current).toBe(false)

    act(() => setVisibility('visible'))
    expect(result.current).toBe(true)
  })

  test('updates on visibilitychange, both directions', () => {
    const { result } = renderHook(() => usePageVisible())

    act(() => setVisibility('hidden'))
    expect(result.current).toBe(false)

    act(() => setVisibility('visible'))
    expect(result.current).toBe(true)

    act(() => setVisibility('hidden'))
    expect(result.current).toBe(false)
  })

  test('registers a passive listener and removes the same handler on unmount', () => {
    const added: Array<{
      type: string
      handler: EventListener
      options?: AddEventListenerOptions
    }> = []
    const removed: EventListener[] = []
    const originalAdd = document.addEventListener.bind(document)
    const originalRemove = document.removeEventListener.bind(document)

    document.addEventListener = ((
      type: string,
      handler: EventListener,
      options?: AddEventListenerOptions,
    ) => {
      added.push({ type, handler, options })
      return originalAdd(type, handler, options)
    }) as typeof document.addEventListener
    document.removeEventListener = ((
      type: string,
      handler: EventListener,
      options?: AddEventListenerOptions,
    ) => {
      removed.push(handler)
      return originalRemove(type, handler, options)
    }) as typeof document.removeEventListener

    try {
      const { unmount } = renderHook(() => usePageVisible())

      const visibilityListeners = added.filter((entry) => entry.type === 'visibilitychange')
      expect(visibilityListeners.length).toBe(1)
      // Passive: the listener never calls preventDefault, and passive listeners
      // cannot block the browser's own visibility handling.
      expect(visibilityListeners[0].options).toEqual({ passive: true })

      unmount()

      // Identical handler reference — a fresh closure would leak the listener.
      expect(removed).toContain(visibilityListeners[0].handler)
    } finally {
      document.addEventListener = originalAdd
      document.removeEventListener = originalRemove
    }
  })
})

describe('useOnPageVisible', () => {
  test('fires once per hidden → visible transition and never on mount', () => {
    const onVisible = mock(() => undefined)
    renderHook(() => useOnPageVisible(onVisible))
    expect(onVisible.mock.calls.length).toBe(0)

    act(() => setVisibility('hidden'))
    expect(onVisible.mock.calls.length).toBe(0)

    act(() => setVisibility('visible'))
    expect(onVisible.mock.calls.length).toBe(1)

    // Already visible: another event is not a transition and must not refresh.
    act(() => document.dispatchEvent(new window.Event('visibilitychange')))
    expect(onVisible.mock.calls.length).toBe(1)

    act(() => setVisibility('hidden'))
    act(() => setVisibility('visible'))
    expect(onVisible.mock.calls.length).toBe(2)
  })

  test('refreshes a hook mounted while hidden as soon as it is shown', () => {
    setVisibility('hidden')
    const onVisible = mock(() => undefined)
    renderHook(() => useOnPageVisible(onVisible))

    expect(onVisible.mock.calls.length).toBe(0)

    act(() => setVisibility('visible'))
    expect(onVisible.mock.calls.length).toBe(1)
  })

  test('invokes the latest callback without needing to re-subscribe', () => {
    const seen: number[] = []
    const { rerender } = renderHook(
      ({ value }: { value: number }) => useOnPageVisible(() => seen.push(value)),
      { initialProps: { value: 1 } },
    )

    rerender({ value: 2 })
    act(() => setVisibility('hidden'))
    act(() => setVisibility('visible'))

    // The callback of the last render ran, so pollers see fresh state/props.
    expect(seen).toEqual([2])
  })
})
