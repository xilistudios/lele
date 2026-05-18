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
  default: 'bg-slate-500/10 text-slate-400 border-slate-500/20',
  primary: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
  success: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20',
  warning: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20',
  error: 'bg-red-500/10 text-red-400 border-red-500/20',
  info: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
}

const SIZE_STYLES: Record<Size, string> = {
  sm: 'text-[10px] px-1.5 py-0.5 gap-1',
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
