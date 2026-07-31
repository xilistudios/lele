import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { getModeList, getModeTheme } from '../../lib/modeTheme'

export function ModeSelector() {
  const { chatMode, onSelectMode, groupsEnabled } = useAppLogicContext()
  const { t } = useTranslation()
  const modes = getModeList(groupsEnabled)

  return (
    <div className="flex rounded-lg bg-surface-tertiary p-0.5 gap-0.5">
      {modes.map((id) => {
        const theme = getModeTheme(id)
        const active = chatMode === id
        const Icon = theme.Icon
        return (
          <button
            key={id}
            type="button"
            onClick={() => onSelectMode(id)}
            aria-pressed={active}
            title={t(theme.descKey)}
            className={`flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium transition-colors ${
              active ? theme.tabActive : 'text-text-secondary hover:text-text-primary'
            }`}
          >
            <Icon size={13} />
            <span>{t(theme.labelKey)}</span>
          </button>
        )
      })}
    </div>
  )
}
