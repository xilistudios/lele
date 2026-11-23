import { useCallback, useEffect, useLayoutEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import type { Attachment } from '../../lib/types'
import { AuthedImage } from './AuthedImage'

type Props = {
  images: Attachment[]
  index: number
  onClose: () => void
  onIndexChange?: (index: number) => void
}

const overlayClass =
  'fixed inset-0 z-50 m-0 flex h-dvh max-h-none w-full max-w-none items-center justify-center border-0 bg-overlay p-4'

const controlClass =
  'pointer-events-auto inline-flex h-9 w-9 items-center justify-center rounded-full border border-border bg-background-primary/90 text-text-primary shadow-card transition-colors hover:bg-background-secondary'

function ChevronLeftIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="15 18 9 12 15 6" />
    </svg>
  )
}

function ChevronRightIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="9 18 15 12 9 6" />
    </svg>
  )
}

export function ImageLightbox({ images, index, onClose, onIndexChange }: Props) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const closeRef = useRef<HTMLButtonElement>(null)
  const previouslyFocused = useRef<HTMLElement | null>(null)

  const total = images.length
  const safeIndex = total === 0 ? 0 : Math.min(Math.max(index, 0), total - 1)
  const current = images[safeIndex]
  const wrap = total > 1

  const goTo = useCallback(
    (next: number) => {
      if (!wrap) return
      onIndexChange?.(((next % total) + total) % total)
    },
    [onIndexChange, total, wrap],
  )

  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  const goToRef = useRef(goTo)
  goToRef.current = goTo

  useLayoutEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (typeof dialog.showModal === 'function') {
      dialog.showModal()
      return
    }
    dialog.setAttribute('open', '')
  }, [])

  useEffect(() => {
    previouslyFocused.current = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    closeRef.current?.focus()

    return () => {
      document.body.style.overflow = previousOverflow
      previouslyFocused.current?.focus?.()
    }
  }, [])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onCloseRef.current()
        return
      }
      if (!wrap) return
      if (event.key === 'ArrowLeft') {
        event.preventDefault()
        goToRef.current(safeIndex - 1)
      }
      if (event.key === 'ArrowRight') {
        event.preventDefault()
        goToRef.current(safeIndex + 1)
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [safeIndex, wrap])

  if (!current) return null

  return createPortal(
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-label={t('chat.viewImage')}
      data-testid="image-lightbox"
      className={overlayClass}
      onCancel={(event) => {
        event.preventDefault()
        onClose()
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
      onKeyDown={(event) => {
        if (event.key === 'Escape') onClose()
      }}
    >
      <div className="pointer-events-none absolute inset-x-0 top-0 flex justify-end p-3">
        <button
          ref={closeRef}
          type="button"
          aria-label={t('common.close')}
          title={t('common.close')}
          className={controlClass}
          onClick={onClose}
        >
          <svg
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            aria-hidden="true"
          >
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      <div className="pointer-events-auto">
        <AuthedImage
          path={current.path ?? ''}
          alt={current.name ?? 'image'}
          className="max-h-[92dvh] max-w-[92vw] rounded-lg object-contain"
        />
      </div>

      {wrap ? (
        <>
          <button
            type="button"
            className={`${controlClass} absolute left-2 top-1/2 -translate-y-1/2`}
            aria-label={t('chat.previousImage')}
            title={t('chat.previousImage')}
            onClick={() => goTo(safeIndex - 1)}
          >
            <ChevronLeftIcon />
          </button>
          <button
            type="button"
            className={`${controlClass} absolute right-2 top-1/2 -translate-y-1/2`}
            aria-label={t('chat.nextImage')}
            title={t('chat.nextImage')}
            onClick={() => goTo(safeIndex + 1)}
          >
            <ChevronRightIcon />
          </button>
        </>
      ) : null}

      {wrap ? (
        <div
          className="pointer-events-none absolute inset-x-0 bottom-0 flex items-center justify-center p-3 text-xs text-text-secondary"
          data-testid="image-lightbox-counter"
        >
          {`${safeIndex + 1} / ${total}`}
        </div>
      ) : null}
    </dialog>,
    document.body,
  )
}
