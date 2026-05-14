import { useEffect, useRef } from 'react'

export function useClickOutside<T extends HTMLElement>(
  handler: () => void,
  enabled: boolean,
) {
  const ref = useRef<T>(null)

  useEffect(() => {
    if (!enabled) return

    const handleClick = (event: MouseEvent) => {
      const el = ref.current
      if (el && !el.contains(event.target as Node)) {
        handler()
      }
    }

    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [enabled, handler])

  return ref
}
