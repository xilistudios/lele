import type { ReactNode } from 'react'
import { RemoveButton } from '../atoms/RemoveButton'

type Props = {
  title: string
  onRemove: () => void
  removeLabel: string
  children: ReactNode
}

const CARD_CLS = 'rounded border border-[#2e2e2e] bg-[#1a1a1a] p-4'

export function NamedItemCard({ title, onRemove, removeLabel, children }: Props) {
  return (
    <div className={CARD_CLS}>
      <div className="mb-3 flex items-center justify-between">
        <span className="font-mono text-xs font-medium text-white">{title}</span>
        <RemoveButton onClick={onRemove} ariaLabel={removeLabel} />
      </div>
      <div className="space-y-3">{children}</div>
    </div>
  )
}
