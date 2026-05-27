import { useTranslation } from 'react-i18next'
import { IconButton } from './IconButton'
import { SubagentsIcon } from './Icons'

interface SubagentsIndicatorProps {
  count: number
  onClick: () => void
}

export function SubagentsIndicator({ count, onClick }: SubagentsIndicatorProps) {
  const { t } = useTranslation()

  return (
    <div className="relative">
      <IconButton
        onClick={onClick}
        className="rounded p-1.5 text-text-tertiary hover:bg-surface-hover hover:text-text-secondary transition-colors duration-150"
        aria-label={t('chat.subagentsTitle')}
      >
        <SubagentsIcon size={18} />
        {count > 0 && (
          <span className="absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-brand-rosa text-[10px] font-medium text-white">
            {count > 9 ? '9+' : count}
          </span>
        )}
      </IconButton>
    </div>
  )
}
