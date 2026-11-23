import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usePopoverPosition } from '../../hooks/usePopoverPosition'

type Props = {
  status: 'disconnected' | 'connecting' | 'connected'
  apiUrl?: string
}

const POPOVER_WIDTH = 180
const POPOVER_HEIGHT = 80

const STATUS_CONFIG = {
  connected: { dot: 'bg-state-success', text: 'text-state-success' },
  connecting: { dot: 'bg-state-warning animate-pulse', text: 'text-state-warning' },
  disconnected: { dot: 'bg-state-error', text: 'text-text-tertiary' },
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
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        aria-label={t('connection.statusAria', { status })}
        aria-expanded={isOpen}
        data-testid="connection-status-dot"
        className="flex h-6 w-6 items-center justify-center rounded-full transition-colors hover:bg-surface-hover"
      >
        <span className={`h-2.5 w-2.5 rounded-full ${config.dot}`} aria-hidden="true" />
      </button>

      <div
        className={`absolute z-50 w-[180px] rounded-lg border border-border bg-background-secondary px-3 py-2 shadow-lg transition-all duration-fast ${verticalClass} ${horizontalClass} ${origin} ${
          isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none'
        }`}
        role="menu"
      >
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${config.dot}`} />
          <span className={`text-xs font-medium ${config.text}`}>{t(`connection.${status}`)}</span>
        </div>
        <div className="mt-2 rounded px-2 py-1 bg-surface-hover text-xs font-medium font-mono text-text-secondary">
          {displayUrl}
        </div>
      </div>
    </div>
  )
}
