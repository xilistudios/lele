import { useEffect } from 'react'

/**
 * Writes --app-vh in px ONLY while an on-screen keyboard shrinks the visual
 * viewport below the layout viewport (iOS Safari — the keyboard never resizes
 * the layout viewport there, so 100dvh alone leaves the composer hidden).
 *
 * On Chrome/Brave Android with interactive-widget=resizes-content the layout
 * viewport already tracks the keyboard, vv.height ≈ window.innerHeight, and
 * this hook removes the override → pure dvh. During pinch-zoom (scale ≠ 1) it
 * also removes the override so the app never shrinks under zoom.
 *
 * The remove-on-equality rule IS the px→dvh handoff: no orientationchange
 * listener is needed (rotation fires resize on both targets), and a stale px
 * value can never outlive the condition that produced it.
 */
const SCALE_EPSILON = 0.01
const HEIGHT_EPSILON = 1

export function useAppViewportHeight(): void {
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return undefined // jsdom / very old browsers: CSS dvh stands alone
    const root = document.documentElement

    const apply = () => {
      const current = window.visualViewport
      if (!current) return
      if (Math.abs(current.scale - 1) > SCALE_EPSILON) {
        root.style.removeProperty('--app-vh') // pinch-zoom: never shrink the app
        return
      }
      const height = Math.round(current.height)
      if (height >= window.innerHeight - HEIGHT_EPSILON) {
        root.style.removeProperty('--app-vh') // no keyboard: dvh is correct
      } else {
        root.style.setProperty('--app-vh', `${height}px`)
      }
    }

    apply()
    vv.addEventListener('resize', apply)
    vv.addEventListener('scroll', apply)
    window.addEventListener('resize', apply)
    return () => {
      vv.removeEventListener('resize', apply)
      vv.removeEventListener('scroll', apply)
      window.removeEventListener('resize', apply)
      root.style.removeProperty('--app-vh')
    }
  }, [])
}
