import type { ReactNode } from 'react'

type Size = 'sm' | 'md' | 'lg' | 'xl' | 'full'

const SIZE_CLASSES: Record<Size, string> = {
  sm: 'max-w-sm',
  md: 'max-w-lg',
  lg: 'max-w-2xl',
  xl: 'max-w-4xl',
  full: 'max-w-[90vw]',
}

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
  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-sm transition-opacity"
        onClick={onClose}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onClose()
        }}
        role="button"
        tabIndex={0}
        aria-label="Close modal"
      />
      <div
        className={`relative z-10 w-full ${SIZE_CLASSES[size]} rounded-xl border border-border bg-background-primary shadow-2xl max-h-[90vh] overflow-hidden`}
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
      >
        <div className="flex items-center justify-between border-b border-border px-6 py-4 bg-background-secondary/50">
          <h2 id="modal-title" className="text-base font-semibold text-text-primary">
            {title}
          </h2>
          {showCloseButton && (
            <button
              type="button"
              onClick={onClose}
              className="flex items-center justify-center w-8 h-8 rounded-lg text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-all duration-200"
              aria-label="Close"
            >
              <svg
                width="18"
                height="18"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          )}
        </div>
        <div className="overflow-y-auto max-h-[calc(90vh-4rem)]">{children}</div>
      </div>
    </div>
  )
}
