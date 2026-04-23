import type { ReactNode } from 'react'

type Props = {
  onClick?: () => void
  title?: string
  disabled?: boolean
  className?: string
  ariaLabel?: string
  children: ReactNode
}

export function IconButton({
  onClick,
  title,
  disabled = false,
  className = '',
  ariaLabel,
  children,
}: Props) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-label={ariaLabel ?? title}
      className={`text-text-tertiary transition-colors hover:text-text-secondary disabled:opacity-50 disabled:cursor-not-allowed ${className}`}
    >
      {children}
    </button>
  )
}
