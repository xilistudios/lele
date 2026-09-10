import { type ReactNode, useCallback, useEffect, useId, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from './IconButton'

type Size = 'sm' | 'md' | 'lg' | 'xl' | 'full'

const SIZE_CLASSES: Record<Size, string> = {
  sm: 'max-w-sm',
  md: 'max-w-lg',
  lg: 'max-w-2xl',
  xl: 'max-w-4xl',
  full: 'max-w-[90vw]',
}

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

type Props = {
  isOpen: boolean
  onClose: () => void
  title: string
  children: ReactNode
  size?: Size
  showCloseButton?: boolean
}

export function Modal({
  isOpen,
  onClose,
  title,
  children,
  size = 'md',
  showCloseButton = true,
}: Props) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  const previouslyFocused = useRef<HTMLElement | null>(null)

  // Keep Tab focus inside the dialog while it is open.
  const trapFocus = useCallback((e: KeyboardEvent) => {
    if (e.key !== 'Tab') return
    const dialog = dialogRef.current
    if (!dialog) return
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    if (focusable.length === 0) {
      e.preventDefault()
      return
    }
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    const active = document.activeElement as HTMLElement | null
    if (e.shiftKey && (active === first || !dialog.contains(active))) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && (active === last || !dialog.contains(active))) {
      e.preventDefault()
      first.focus()
    }
  }, [])

  useEffect(() => {
    if (!isOpen) return

    // Remember the previously focused element so we can restore it on close.
    previouslyFocused.current = document.activeElement as HTMLElement | null

    // Lock body scroll while the modal is open.
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    // Move initial focus into the dialog.
    const dialog = dialogRef.current
    if (dialog) {
      const focusable = dialog.querySelector<HTMLElement>(FOCUSABLE_SELECTOR)
      ;(focusable ?? dialog).focus()
    }

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      trapFocus(e)
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      document.body.style.overflow = prevOverflow
      previouslyFocused.current?.focus?.()
    }
  }, [isOpen, onClose, trapFocus])

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      {/* Decorative backdrop: Escape (document listener) is the keyboard path to close. */}
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-sm transition-opacity"
        onClick={onClose}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onClose()
        }}
        aria-hidden="true"
      />
      <dialog
        ref={dialogRef}
        open
        tabIndex={-1}
        className={`relative z-10 w-full ${SIZE_CLASSES[size]} rounded-xl border border-border bg-background-primary shadow-2xl max-h-[90vh] overflow-hidden outline-none`}
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <div className="flex items-center justify-between border-b border-border px-6 py-4 bg-background-secondary/50">
          <h2 id={titleId} className="text-base font-semibold text-text-primary">
            {title}
          </h2>
          {showCloseButton && (
            <IconButton
              onClick={onClose}
              variant="ghost"
              ariaLabel={t('common.close')}
              title={t('common.close')}
              className="flex items-center justify-center w-8 h-8"
            >
              <svg
                width="18"
                height="18"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>{t('common.close')}</title>
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </IconButton>
          )}
        </div>
        <div className="overflow-y-auto max-h-[calc(90vh-4rem)]">{children}</div>
      </dialog>
    </div>
  )
}
