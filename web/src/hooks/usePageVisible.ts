import { useEffect, useRef, useState } from 'react'

export function isPageVisible(): boolean {
  if (typeof document === 'undefined') return true
  return document.visibilityState !== 'hidden'
}

export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(isPageVisible)

  useEffect(() => {
    if (typeof document === 'undefined') return

    const onVisibilityChange = () => setVisible(isPageVisible())
    onVisibilityChange()
    document.addEventListener('visibilitychange', onVisibilityChange, { passive: true })

    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [])

  return visible
}

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
