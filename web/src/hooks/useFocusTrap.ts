import { type RefObject, useEffect } from 'react'

export const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

const isFocusable = (element: HTMLElement): boolean => {
  if (element.hidden) return false
  if (element.getAttribute('aria-hidden') === 'true') return false
  if (element.closest('[hidden], [inert], [aria-hidden="true"]')) return false
  if (element instanceof HTMLInputElement && element.type === 'hidden') return false
  if (element.tabIndex < 0) return false

  const style = window.getComputedStyle(element)
  if (style.display === 'none' || style.visibility === 'hidden') return false

  return true
}

export function focusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(isFocusable)
}

/**
 * Keeps Tab / Shift+Tab cycling inside `containerRef` while `active` is true,
 * so keyboard users cannot reach the page behind an open dialog.
 */
export function useFocusTrap(containerRef: RefObject<HTMLElement | null>, active: boolean) {
  useEffect(() => {
    if (!active) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return
      const container = containerRef.current
      if (!container) return

      const focusable = focusableElements(container)
      if (focusable.length === 0) {
        event.preventDefault()
        return
      }

      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      const current = document.activeElement as HTMLElement | null
      const outside = !container.contains(current)

      if (event.shiftKey && (current === first || outside)) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && (current === last || outside)) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [containerRef, active])
}
