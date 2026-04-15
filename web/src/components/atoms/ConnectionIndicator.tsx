import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usePopoverPosition } from '../../hooks/usePopoverPosition'
import { ServerIcon } from './Icons'

type Props = {
  status: 'disconnected' | 'connecting' | 'connected'
  apiUrl?: string
}

const POPOVER_WIDTH = 180
const POPOVER_HEIGHT = 80

const STATUS_CONFIG = {
  connected: { dot: 'bg-emerald-400', text: 'text-emerald-400' },
  connecting: { dot: 'bg-yellow-400 animate-pulse-dot', text: 'text-yellow-400' },
  disconnected: { dot: 'bg-red-400', text: 'text-gray-500' },
} as const

const ORIGIN_MAP = {
  below: { 'right-align': 'origin-top-right', 'left-align': 'origin-top-left' },
  above: { 'right-align': 'origin-bottom-right', 'left-align': 'origin-bottom-left' },
} as const

export function ConnectionIndicator({ status, apiUrl }: Props) {
  const { t } = useTranslation()
  const [isOpen, setIsOpen] = useState(false)
  const { position, ref } = usePopoverPosition({
    isOpen,
    popoverWidth: POPOVER_WIDTH,
    popoverHeight: POPOVER_HEIGHT,
  })

  const config = STATUS_CONFIG[status]
  const displayUrl = apiUrl?.replace(/^https?:\/\//, '') ?? 'N/A'

  useEffect(() => {
    if (!isOpen) return

    const onClickOutside = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setIsOpen(false)
      }
    }

    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [isOpen, ref])

  const origin = ORIGIN_MAP[position.vertical][position.horizontal]
  const verticalClass = position.vertical === 'below' ? 'top-full mt-1' : 'bottom-full mb-1'
  const horizontalClass = position.horizontal === 'right-align' ? 'right-0' : 'left-0'

  return (
    <div ref={ref} className={`relative ${!isOpen ? 'group' : ''}`}>
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="rounded p-1.5 text-gray-500 hover:bg-gray-700/50 hover:text-gray-400 transition-colors duration-150"
        aria-label={t('connection.statusAria', { status })}
      >
        <div className="relative">
          <ServerIcon />
          <span
            className={`absolute -top-1 -right-1 h-2.5 w-2.5 rounded-full border border-[#1a1a1a] ${config.dot}`}
            aria-hidden="true"
          />
        </div>
      </button>

      <span
        className={`absolute top-full left-1/2 -translate-x-1/2 mt-1 px-2 py-1 rounded bg-[#2a2a2a] text-xs text-[#ccc] transition-opacity duration-100 pointer-events-none whitespace-nowrap ${
          isOpen ? 'opacity-0' : 'opacity-0 group-hover:opacity-100'
        }`}
      >
        {t('common.status')}
      </span>

      <div
        className={`absolute z-50 w-[180px] rounded-lg border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 shadow-lg transition-all duration-150 ${verticalClass} ${horizontalClass} ${origin} ${
          isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none'
        }`}
        role="menu"
      >
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${config.dot}`} />
          <span className={`text-xs font-medium ${config.text}`}>{t(`connection.${status}`)}</span>
        </div>
        <div className="mt-2 rounded bg-[#2a2a2a] px-2 py-1.5 text-xs font-mono text-[#ccc]">
          {displayUrl}
        </div>
      </div>
    </div>
  )
}
