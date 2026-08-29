export const BUTTON_BASE =
  'inline-flex items-center justify-center rounded-md font-medium transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-[color-mix(in_srgb,var(--color-accent-primary)_50%,transparent)] focus:ring-offset-2 focus:ring-offset-background-primary'

export const BUTTON_SIZES = {
  sm: 'px-2 py-1 text-xs',
  md: 'px-3 py-2 text-sm',
  lg: 'px-4 py-2.5 text-base',
} as const

export const BUTTON_VARIANTS = {
  primary: 'bg-accent-primary text-text-on-accent hover:bg-accent-hover',
  secondary:
    'border border-border bg-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary',
  danger: 'bg-red-600 text-white hover:bg-red-500',
  dangerText: 'text-state-error hover:bg-red-500/10 hover:text-red-400',
  ghost: 'bg-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary',
  nav: 'text-text-secondary hover:bg-surface-hover hover:text-text-primary',
  success: 'bg-state-success text-text-on-accent hover:bg-state-success/80',
  brand: 'bg-brand-rosa text-white hover:bg-brand-rosa/90',
  blue: 'bg-blue-600 text-white hover:bg-blue-500',
} as const

export const BUTTON_DISABLED = 'disabled:opacity-40 disabled:cursor-not-allowed'

export const BUTTON_FOCUS =
  'focus:outline-none focus:ring-2 focus:ring-[color-mix(in_srgb,var(--color-accent-primary)_50%,transparent)] focus:ring-offset-2 focus:ring-offset-background-primary'

export function getButtonClasses({
  variant = 'primary',
  size = 'md',
  disabled = false,
  className = '',
}: {
  variant?: keyof typeof BUTTON_VARIANTS
  size?: keyof typeof BUTTON_SIZES
  disabled?: boolean
  className?: string
} = {}): string {
  const parts = [BUTTON_BASE, BUTTON_SIZES[size], BUTTON_VARIANTS[variant]]

  if (disabled) {
    parts.push(BUTTON_DISABLED)
  }

  if (className) {
    parts.push(className)
  }

  return parts.join(' ')
}
