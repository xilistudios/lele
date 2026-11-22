import { useEffect } from 'react'

const SCALE_EPSILON = 0.01
const HEIGHT_EPSILON = 1

export function useAppViewportHeight(): void {
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return undefined
    const root = document.documentElement

    const apply = () => {
      const current = window.visualViewport
      if (!current) return
      if (Math.abs(current.scale - 1) > SCALE_EPSILON) {
        root.style.removeProperty('--app-vh')
        return
      }
      const height = Math.round(current.height)
      if (height >= window.innerHeight - HEIGHT_EPSILON) {
        root.style.removeProperty('--app-vh')
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
