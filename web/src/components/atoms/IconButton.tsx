import type { ReactNode } from 'react'

const variantStyles: Record<string, string> = {
  default:
    'rounded p-1.5 text-text-tertiary transition-colors hover:text-text-secondary hover:bg-surface-hover',
  nav: 'flex items-center justify-center rounded-md text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary',
  'nav-full':
    'flex items-center gap-2 w-full rounded-md px-2 py-2 text-sm text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary',
  danger: 'rounded p-1.5 text-text-tertiary transition-colors hover:text-[#FF7B7B] hover:bg-red-500/10',
  ghost: 'rounded p-1.5 text-text-tertiary transition-colors hover:bg-background-tertiary hover:text-text-primary',
}

type Props = {
  onClick?: () => void
  title?: string
  disabled?: boolean
  variant?: 'default' | 'nav' | 'nav-full' | 'danger' | 'ghost'
  className?: string
  ariaLabel?: string
  children: ReactNode
}

export function IconButton({
  onClick,
  title,
  disabled = false,
  variant = 'default',
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
      className={`${variantStyles[variant]} disabled:opacity-40 disabled:cursor-not-allowed ${className}`}
    >
      {children}
    </button>
  )
}
