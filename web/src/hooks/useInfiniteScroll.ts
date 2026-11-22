import { useCallback, useEffect, useState } from 'react'

type Options = {
  total: number
  pageSize: number
  resetKey?: unknown
}

const PRELOAD_MARGIN = '160px'

/**
 * Reveals `pageSize` extra items every time the returned `sentinelRef` enters
 * the viewport, so long lists can be rendered incrementally.
 */
export function useInfiniteScroll({ total, pageSize, resetKey }: Options) {
  const [requested, setRequested] = useState(pageSize)
  const [sentinel, setSentinel] = useState<HTMLElement | null>(null)

  // biome-ignore lint/correctness/useExhaustiveDependencies: resetKey is the trigger, not a read — a new scope must start back at one page
  useEffect(() => {
    setRequested(pageSize)
  }, [pageSize, resetKey])

  const loadMore = useCallback(() => {
    setRequested((current) => current + pageSize)
  }, [pageSize])

  const visibleCount = Math.min(requested, total)
  const hasMore = visibleCount < total

  // biome-ignore lint/correctness/useExhaustiveDependencies: visibleCount re-observes the sentinel after each page so it can keep loading while a tall viewport still shows it
  useEffect(() => {
    if (!sentinel || !hasMore) return
    if (typeof IntersectionObserver === 'undefined') return

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) loadMore()
      },
      { rootMargin: `0px 0px ${PRELOAD_MARGIN} 0px` },
    )
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [sentinel, hasMore, loadMore, visibleCount])

  const sentinelRef = useCallback((node: HTMLElement | null) => setSentinel(node), [])

  return { visibleCount, hasMore, sentinelRef, loadMore }
}
