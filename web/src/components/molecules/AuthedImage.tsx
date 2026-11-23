import { type RefObject, useEffect, useRef, useState } from 'react'
import { useAuthContext } from '../../contexts/AuthContext'

type AuthedImageProps = {
  path: string
  alt: string
  className?: string
}

const MAX_CACHE_ENTRIES = 50

const urlCache = new Map<string, string>()
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

export function clearAuthedImageCache(): void {
  for (const url of urlCache.values()) {
    URL.revokeObjectURL(url)
  }
  urlCache.clear()
  inflight.clear()
}

function useIntersectOnce(ref: RefObject<HTMLElement | null>): boolean {
  const [intersected, setIntersected] = useState(() => {
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

type LoadedImage = {
  path: string
  url: string
}

export function AuthedImage({ path, alt, className }: AuthedImageProps) {
  const { api, apiUrl } = useAuthContext()
  const containerRef = useRef<HTMLSpanElement>(null)
  const inView = useIntersectOnce(containerRef)

  const [loaded, setLoaded] = useState<LoadedImage | null>(null)
  const [failedPath, setFailedPath] = useState<string | null>(null)

  const fileName = path ? (path.split('/').pop() ?? alt) : alt
  const ready = loaded?.path === path ? loaded : null
  const failed = path !== '' && failedPath === path

  useEffect(() => {
    if (!path || !inView) return

    let cancelled = false
    const key = cacheKey(apiUrl, path)
    const cached = urlCache.get(key)
    if (cached) {
      setLoaded({ path, url: cached })
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

    promise.then(
      (url) => {
        if (!cancelled) setLoaded({ path, url })
      },
      () => {
        if (!cancelled) setFailedPath(path)
      },
    )

    return () => {
      cancelled = true
    }
  }, [api, apiUrl, path, inView])

  if (!path || failed) {
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

  if (!ready) {
    return <span ref={containerRef} className={className} data-state="loading" aria-busy="true" />
  }

  return <img src={ready.url} alt={alt} className={className} loading="lazy" />
}
