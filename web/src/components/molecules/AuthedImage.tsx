// Authenticated image component for workspace attachment previews.
//
// The public /api/v1/files/view endpoint (restricted in PR 305) only serves
// staging dirs; workspace-persisted attachments return 403. A plain <img src>
// cannot carry an Authorization header, so this component fetches the blob
// through the authenticated view-secure endpoint and converts it to an object URL.

import { type RefObject, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../../contexts/AuthContext'

type AuthedImageProps = {
  path: string
  alt: string
  /** Classes applied to the <img> and the loading placeholder. */
  className?: string
}

const MAX_CACHE_ENTRIES = 50

/** Cache keyed by `${apiUrl}\u0000${path}` → object URL. */
const urlCache = new Map<string, string>()
/** In-flight requests for deduplication. */
const inflight = new Map<string, Promise<string>>()

function cacheKey(apiUrl: string, path: string): string {
  return `${apiUrl}\u0000${path}`
}

function evictOldest() {
  const first = urlCache.keys().next().value
  if (first !== undefined) {
    const url = urlCache.get(first)
    if (url) URL.revokeObjectURL(url)
    urlCache.delete(first)
  }
}

function setCache(key: string, url: string) {
  if (urlCache.size >= MAX_CACHE_ENTRIES) evictOldest()
  urlCache.set(key, url)
}

/**
 * @internal — clears the module-level image cache. Intended for tests.
 */
export function clearAuthedImageCache(): void {
  for (const url of urlCache.values()) {
    URL.revokeObjectURL(url)
  }
  urlCache.clear()
  inflight.clear()
}

function useIntersectOnce(ref: RefObject<HTMLElement | null>): boolean {
  const [intersected, setIntersected] = useState(() => {
    // No IntersectionObserver → load immediately (jsdom, old browsers).
    if (typeof IntersectionObserver === 'undefined') return true
    return false
  })

  useEffect(() => {
    if (intersected) return
    const el = ref.current
    if (!el) return

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            observer.disconnect()
            setIntersected(true)
            return
          }
        }
      },
      { rootMargin: '200px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [ref, intersected])

  return intersected
}

export function AuthedImage({ path, alt, className }: AuthedImageProps) {
  const { api, apiUrl } = useAuthContext()
  const containerRef = useRef<HTMLSpanElement>(null)
  const inView = useIntersectOnce(containerRef)

  const [state, setState] = useState<'loading' | 'ready' | 'error'>(path ? 'loading' : 'error')
  const [objectUrl, setObjectUrl] = useState<string | null>(null)

  const fileName = path ? (path.split('/').pop() ?? alt) : alt

  useEffect(() => {
    if (!path || !inView) return

    let cancelled = false
    const key = cacheKey(apiUrl, path)
    const cached = urlCache.get(key)
    if (cached) {
      if (!cancelled) {
        setObjectUrl(cached)
        setState('ready')
      }
      return
    }

    let promise = inflight.get(key)
    if (!promise) {
      promise = api.fileBlob(path).then(
        (blob) => {
          const url = URL.createObjectURL(blob)
          setCache(key, url)
          inflight.delete(key)
          return url
        },
        (error: unknown) => {
          inflight.delete(key)
          throw error
        },
      )
      inflight.set(key, promise)
    }

    promise
      .then((url) => {
        if (!cancelled) {
          setObjectUrl(url)
          setState('ready')
        }
      })
      .catch(() => {
        if (!cancelled) setState('error')
      })

    return () => {
      cancelled = true
    }
  }, [api, apiUrl, path, inView])

  if (!path || state === 'error') {
    return (
      <span
        title={path}
        data-state="error"
        className="rounded-full border border-border bg-background-secondary px-3 py-1 text-xs text-text-primary"
      >
        {fileName}
      </span>
    )
  }

  if (state === 'loading') {
    return <span ref={containerRef} className={className} data-state="loading" aria-busy="true" />
  }

  return <img src={objectUrl ?? undefined} alt={alt} className={className} loading="lazy" />
}
