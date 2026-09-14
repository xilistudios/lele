import type { ReactNode } from 'react'

type Variant = 'default' | 'primary' | 'success' | 'warning' | 'error' | 'info'
type Size = 'sm' | 'md'

type Props = {
  variant?: Variant
  size?: Size
  bordered?: boolean
  children: ReactNode
  className?: string
}

const VARIANT_STYLES: Record<Variant, string> = {
  default: 'bg-background-tertiary text-text-tertiary border-border',
  primary: 'bg-state-info/10 text-state-info border-state-info',
  success: 'bg-state-success/10 text-state-success border-state-success',
  warning: 'bg-state-warning/10 text-state-warning border-state-warning',
  error: 'bg-state-error/10 text-state-error border-state-error',
  info: 'bg-state-info/10 text-state-info border-state-info',
}

const SIZE_STYLES: Record<Size, string> = {
  sm: 'text-2xs px-1.5 py-0.5 gap-1',
  md: 'text-xs px-2 py-0.5 gap-1.5',
}

export function Badge({
  variant = 'default',
  size = 'sm',
  bordered = false,
  children,
  className = '',
}: Props) {
  return (
    <span
      className={`inline-flex items-center rounded-md font-medium ${SIZE_STYLES[size]} ${VARIANT_STYLES[variant]} ${bordered ? 'border' : 'border-0'} ${className}`}
    >
      {children}
    </span>
  )
}
