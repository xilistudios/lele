import { useEffect, useRef, useState } from 'react'

/**
 * Page-visibility primitives for the WebUI's interval pollers.
 *
 * WHY: the pollers (backend status, cron jobs, subagents, chat history) hit
 * expensive gateway endpoints from a timer that keeps ticking while the tab sits
 * in the background. React Query already skips the FETCH of an interval tick
 * that fires while the document is unfocused (`focusManager.isFocused()` gate in
 * @tanstack/query-core's queryObserver), so for its own poller the gate below is
 * defence in depth; the plain `setInterval` pollers — which have no such
 * protection — are where the requests are actually saved. What everything below
 * guarantees for all of them: a hidden tab runs no timer, issues zero requests,
 * and refreshes exactly ONCE the moment it is shown again instead of waiting up
 * to a whole interval for the next tick.
 */

/**
 * True when the document is visible. Plain (non-hook) helper so interval
 * callbacks can re-check without subscribing to React state.
 *
 * SSR/test safe: with no `document` there is no hidden browser tab to protect,
 * so the caller is treated as visible (the previous behaviour).
 */
export function isPageVisible(): boolean {
  if (typeof document === 'undefined') return true
  return document.visibilityState !== 'hidden'
}

/**
 * Reactive form of `isPageVisible()`: the returned flag updates on every
 * `visibilitychange`, which is what pollers use to gate their intervals.
 */
export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(isPageVisible)

  useEffect(() => {
    if (typeof document === 'undefined') return

    const onVisibilityChange = () => setVisible(isPageVisible())
    // Re-read once on mount: visibility can flip between the first render and
    // the effect running (e.g. the tab is backgrounded while it hydrates).
    onVisibilityChange()
    document.addEventListener('visibilitychange', onVisibilityChange, { passive: true })

    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [])

  return visible
}

/**
 * Runs `onVisible` once per hidden → visible transition (never on mount).
 *
 * Pollers clear their interval while hidden, so without this the first update
 * after returning to the tab would wait a full interval — and work that
 * finished while the tab was hidden (or a WebSocket event dropped during that
 * period) would stay invisible. The callback is read through a ref so it always
 * sees the latest state/props without re-subscribing on every render.
 */
export function useOnPageVisible(onVisible: () => void): void {
  const onVisibleRef = useRef(onVisible)
  onVisibleRef.current = onVisible

  const visible = usePageVisible()
  const wasVisibleRef = useRef(visible)

  useEffect(() => {
    const becameVisible = visible && !wasVisibleRef.current
    wasVisibleRef.current = visible
    if (becameVisible) onVisibleRef.current()
  }, [visible])
}
