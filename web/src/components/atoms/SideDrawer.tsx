import { type ReactNode, useEffect, useRef, useState } from 'react'
import { useFocusTrap } from '../../hooks/useFocusTrap'

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
  const [mounted, setMounted] = useState(isOpen)
  const [shown, setShown] = useState(isOpen)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const invokerRef = useRef<HTMLElement | null>(null)

  useFocusTrap(dialogRef, mounted && isOpen)

  useEffect(() => {
    if (!isOpen) return
    const active = document.activeElement
    invokerRef.current = active instanceof HTMLElement && active !== document.body ? active : null
  }, [isOpen])

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
    if (!mounted || !isOpen) return
    const dialog = dialogRef.current
    if (dialog && !dialog.contains(document.activeElement)) dialog.focus()
  }, [mounted, isOpen])

  useEffect(() => {
    if (isOpen) return
    const invoker = invokerRef.current
    invokerRef.current = null
    const active = document.activeElement
    const dialog = dialogRef.current
    const focusMovedElsewhere =
      active instanceof HTMLElement &&
      active !== document.body &&
      !(dialog?.contains(active) ?? false)

    if (focusMovedElsewhere) return
    if (invoker?.isConnected) invoker.focus()
  }, [isOpen])

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
      <div
        aria-hidden="true"
        className={`fixed inset-0 z-40 bg-overlay transition-opacity duration-300 ease-out ${backdropState}`}
        onClick={onClose}
        onKeyDown={(event) => {
          if (event.key === 'Escape') onClose()
        }}
      />

      <dialog
        ref={dialogRef}
        open
        aria-modal="true"
        tabIndex={-1}
        data-testid={testId}
        aria-label={ariaLabel}
        className={`m-0 bg-background-secondary fixed right-0 top-0 z-50 h-full max-h-none max-md:h-dvh ${widthClassName} max-md:w-full max-w-[90vw] max-md:max-w-full safe-area-y border-l border-border p-0 shadow-lg flex flex-col outline-none transition-transform duration-300 ease-out ${panelState}`}
      >
        {children}
      </dialog>
    </>
  )
}
