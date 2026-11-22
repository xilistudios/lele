import { type ReactNode, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

const ANIMATION_DURATION_MS = 300

type SideDrawerProps = {
  isOpen: boolean
  onClose: () => void
  ariaLabel: string
  children: ReactNode
  widthClassName?: string
  testId?: string
}

export function SideDrawer({
  isOpen,
  onClose,
  ariaLabel,
  children,
  widthClassName = 'w-80',
  testId,
}: SideDrawerProps) {
  const { t } = useTranslation()
  const [mounted, setMounted] = useState(isOpen)
  const [shown, setShown] = useState(isOpen)

  useEffect(() => {
    if (isOpen) {
      setMounted(true)
      return
    }
    setShown(false)
    if (!mounted) return
    const timer = setTimeout(() => setMounted(false), ANIMATION_DURATION_MS)
    return () => clearTimeout(timer)
  }, [isOpen, mounted])

  useEffect(() => {
    if (!mounted || !isOpen) return
    let inner = 0
    const outer = window.requestAnimationFrame(() => {
      inner = window.requestAnimationFrame(() => setShown(true))
    })
    return () => {
      window.cancelAnimationFrame(outer)
      window.cancelAnimationFrame(inner)
    }
  }, [mounted, isOpen])

  useEffect(() => {
    if (!isOpen) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [isOpen, onClose])

  if (!mounted) return null

  const backdropState = shown ? 'opacity-100' : 'opacity-0 pointer-events-none'
  const panelState = shown ? 'translate-x-0' : 'translate-x-full'

  return (
    <>
      <button
        type="button"
        className={`fixed inset-0 z-40 bg-overlay transition-opacity duration-300 ease-out ${backdropState}`}
        onClick={onClose}
        aria-label={t('common.close')}
      />

      <aside
        data-testid={testId}
        aria-label={ariaLabel}
        className={`bg-background-secondary fixed right-0 top-0 z-50 h-full ${widthClassName} max-w-[90vw] safe-area-y border-l border-border shadow-lg flex flex-col transition-transform duration-300 ease-out ${panelState}`}
      >
        {children}
      </aside>
    </>
  )
}
