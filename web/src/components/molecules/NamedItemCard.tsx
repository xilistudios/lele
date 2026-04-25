import type { ReactNode } from 'react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronLeftIcon } from '../atoms/Icons'
import { RemoveButton } from '../atoms/RemoveButton'

type Props = {
  title: ReactNode
  onRemove: () => void
  removeLabel: string
  children: ReactNode
  defaultCollapsed?: boolean
}

const CARD_CLS =
  'rounded-lg border border-border/60 bg-surface-secondary shadow-sm mt-4 first:mt-0 overflow-hidden'

export function NamedItemCard({
  title,
  onRemove,
  removeLabel,
  children,
  defaultCollapsed = true,
}: Props) {
  const [collapsed, setCollapsed] = useState(defaultCollapsed)
  const { t } = useTranslation()

  return (
    <div className={CARD_CLS}>
      <div className="px-5 py-4 flex items-center gap-3 border-b border-border/40">
        <button
          type="button"
          onClick={() => setCollapsed(!collapsed)}
          className="p-1 rounded hover:bg-surface-hover text-text-tertiary hover:text-text-secondary transition-colors flex-shrink-0"
          aria-label={collapsed ? t('common.expand') : t('common.collapse')}
        >
          <ChevronLeftIcon
            className={`transition-transform duration-150 ${collapsed ? 'rotate-90' : '-rotate-90'}`}
          />
        </button>
        <span className="font-medium text-sm text-text-primary flex-1">{title}</span>
        <RemoveButton onClick={onRemove} ariaLabel={removeLabel} />
      </div>

      <div
        className={`grid transition-all duration-200 ${collapsed ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]'}`}
      >
        <div className="overflow-hidden">
          <div className="p-5 space-y-4">{children}</div>
        </div>
      </div>
    </div>
  )
}
