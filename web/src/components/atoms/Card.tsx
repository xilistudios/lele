import type { ReactNode } from 'react'

type Props = {
  children: ReactNode
  className?: string
}

const CARD_CLS = 'rounded border border-border bg-background-primary p-4'

export function Card({ children, className = '' }: Props) {
  return <div className={`${CARD_CLS} ${className}`}>{children}</div>
}
