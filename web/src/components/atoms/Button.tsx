import { type ButtonHTMLAttributes, type ReactNode, forwardRef } from 'react'
import { Spinner } from './Spinner'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
export type ButtonSize = 'sm' | 'md' | 'lg'

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant
  size?: ButtonSize
  /** Shows a spinner and disables the button. */
  loading?: boolean
  children?: ReactNode
}

const baseClasses =
  'inline-flex items-center justify-center gap-2 rounded-lg font-medium transition-colors duration-fast focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus disabled:cursor-not-allowed disabled:opacity-40'

const variantClasses: Record<ButtonVariant, string> = {
  primary:
    'bg-accent-primary text-text-on-accent shadow-sm hover:bg-accent-hover active:bg-accent-active',
  secondary:
    'border border-border bg-background-tertiary text-text-primary hover:bg-surface-hover',
  ghost: 'bg-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary',
  // §5 spec: relleno destructivo = state-error-fill (blanco 4.95 D / 5.74 L).
  // bg-state-error era ❌ 2.77 en oscuro (#F87171 es texto de error, no superficie).
  danger: 'bg-state-error-fill text-text-on-accent hover:opacity-90',
}

const sizeClasses: Record<ButtonSize, string> = {
  sm: 'px-2.5 py-1.5 text-xs',
  md: 'px-4 py-2 text-sm',
  lg: 'px-5 py-2.5 text-sm',
}

/**
 * Standard button atom. Use instead of inline button class strings.
 *
 * @example
 * <Button variant="primary" size="md" loading={isSaving}>Save</Button>
 */
export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  {
    variant = 'primary',
    size = 'md',
    loading = false,
    disabled,
    className = '',
    children,
    ...rest
  },
  ref,
) {
  const isDisabled = disabled || loading
  return (
    <button
      ref={ref}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      className={`${baseClasses} ${variantClasses[variant]} ${sizeClasses[size]} ${className}`}
      {...rest}
    >
      {loading && <Spinner size="sm" />}
      {children}
    </button>
  )
})
