import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { usePopoverPosition } from '../../hooks/usePopoverPosition'
import type { SessionContextResponse } from '../../lib/types'

const POPOVER_WIDTH = 220
const POPOVER_HEIGHT = 180
const CIRCLE_SIZE = 18
const STROKE_WIDTH = 3
const RADIUS = (CIRCLE_SIZE - STROKE_WIDTH) / 2
const CIRCUMFERENCE = 2 * Math.PI * RADIUS

const ORIGIN_MAP = {
  below: { 'right-align': 'origin-top-right', 'left-align': 'origin-top-left' },
  above: { 'right-align': 'origin-bottom-right', 'left-align': 'origin-bottom-left' },
} as const

const REFRESH_INTERVAL = 30_000 // 30 seconds

function getRingColor(percent: number): string {
  if (percent >= 90) return 'stroke-red-400'
  if (percent >= 70) return 'stroke-yellow-400'
  if (percent >= 50) return 'stroke-blue-400'
  return 'stroke-emerald-400'
}

function getTextColor(percent: number): string {
  if (percent >= 90) return 'text-red-400'
  if (percent >= 70) return 'text-yellow-400'
  if (percent >= 50) return 'text-blue-400'
  return 'text-emerald-400'
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

export function ContextIndicator() {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { currentSessionKey, modelState } = useAppLogicContext()
  const [isOpen, setIsOpen] = useState(false)
  const [context, setContext] = useState<SessionContextResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const { position, ref } = usePopoverPosition({
    isOpen,
    popoverWidth: POPOVER_WIDTH,
    popoverHeight: POPOVER_HEIGHT,
  })

  const fetchContext = useCallback(async () => {
    if (!currentSessionKey) {
      setContext(null)
      return
    }
    setLoading(true)
    try {
      const data = await api.sessionContext(currentSessionKey)
      setContext(data)
    } catch {
      // Silently fail — context indicator is non-critical
      setContext(null)
    } finally {
      setLoading(false)
    }
  }, [currentSessionKey, api, modelState.current])

  // Fetch on mount and when session changes
  useEffect(() => {
    fetchContext()
  }, [fetchContext])

  // Poll constantly in the background to keep indicator updated
  useEffect(() => {
    intervalRef.current = setInterval(fetchContext, REFRESH_INTERVAL)
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
    }
  }, [fetchContext])

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

  const percent = context ? Math.round(context.usage_percent * 10) / 10 : 0
  const ringColorClass = context ? getRingColor(context.usage_percent) : 'stroke-gray-500'
  const textColorClass = context ? getTextColor(context.usage_percent) : 'text-gray-500'
  const hasData = context && context.context_window > 0

  // Calculate the dash offset for the SVG ring
  const dashOffset = hasData ? CIRCUMFERENCE * (1 - context.usage_percent / 100) : CIRCUMFERENCE

  const origin = ORIGIN_MAP[position.vertical][position.horizontal]
  const verticalClass = position.vertical === 'below' ? 'top-full mt-1' : 'bottom-full mb-1'
  const horizontalClass = position.horizontal === 'right-align' ? 'right-0' : 'left-0'

  return (
    <div ref={ref} className={`relative ${!isOpen ? 'group' : ''}`}>
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="rounded p-1.5 text-gray-500 hover:bg-gray-700/50 hover:text-gray-400 transition-colors duration-150"
        aria-label={t('connection.context')}
      >
        {/* Circular progress ring — always visible */}
        <svg width={CIRCLE_SIZE} height={CIRCLE_SIZE} className="-rotate-90" aria-hidden="true">
          {/* Background circle (total context) */}
          <circle
            cx={CIRCLE_SIZE / 2}
            cy={CIRCLE_SIZE / 2}
            r={RADIUS}
            fill="none"
            stroke="currentColor"
            strokeWidth={STROKE_WIDTH}
            className="text-gray-600"
          />
          {/* Progress circle (used context) */}
          <circle
            cx={CIRCLE_SIZE / 2}
            cy={CIRCLE_SIZE / 2}
            r={RADIUS}
            fill="none"
            strokeWidth={STROKE_WIDTH}
            strokeLinecap="round"
            strokeDasharray={CIRCUMFERENCE}
            strokeDashoffset={dashOffset}
            className={`transition-all duration-500 ease-out ${ringColorClass}`}
          />
        </svg>
      </button>

      {/* Tooltip on hover */}
      {hasData && (
        <span
          className={`absolute top-full left-1/2 -translate-x-1/2 mt-1 px-2 py-1 rounded bg-surface-card text-xs transition-opacity duration-100 pointer-events-none whitespace-nowrap ${
            isOpen ? 'opacity-0' : 'opacity-0 group-hover:opacity-100'
          }`}
        >
          <span className={textColorClass}>{percent}%</span> usado
        </span>
      )}

      {/* Popover with detailed info */}
      <div
        className={`absolute z-50 w-[220px] rounded-lg border border-border bg-background-primary px-3 py-2.5 shadow-lg transition-all duration-150 ${verticalClass} ${horizontalClass} ${origin} ${
          isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none'
        }`}
        role="menu"
      >
        {!hasData ? (
          <div className="text-xs text-text-tertiary text-center py-1">
            {loading ? t('common.loading') : t('common.noData')}
          </div>
        ) : (
          <>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-medium text-text-secondary">
                {t('connection.context')}
              </span>
              <span className={`text-xs font-mono font-medium ${textColorClass}`}>{percent}%</span>
            </div>

            {/* Progress bar */}
            <div className="h-1.5 w-full rounded-full bg-surface-card mb-2 overflow-hidden">
              <div
                className={`h-full rounded-full transition-all duration-300 ${ringColorClass.replace('stroke-', 'bg-')}`}
                style={{ width: `${Math.min(percent, 100)}%` }}
              />
            </div>

            <div className="space-y-1 text-[11px] text-text-tertiary font-mono">
              <div className="flex justify-between">
                <span>{t('connection.currentContext')}</span>
                <span>{formatTokens(context.total_tokens)}</span>
              </div>
              <div className="flex justify-between">
                <span>{t('connection.contextWindow')}</span>
                <span>{formatTokens(context.context_window)}</span>
              </div>
              {context.cumulative_total_tokens > 0 && (
                <>
                  <div className="border-t border-border pt-1.5 mt-1">
                    <div className="flex justify-between">
                      <span className="text-text-tertiary">{t('connection.cumulativeInput')}</span>
                      <span>{formatTokens(context.cumulative_input_tokens)}</span>
                    </div>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-text-tertiary">{t('connection.cumulativeOutput')}</span>
                    <span>{formatTokens(context.cumulative_output_tokens)}</span>
                  </div>
                  <div className="flex justify-between border-t border-border pt-1 mt-1">
                    <span className="text-text-tertiary">{t('connection.cumulativeTotal')}</span>
                    <span className="text-text-secondary">
                      {formatTokens(context.cumulative_total_tokens)}
                    </span>
                  </div>
                </>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
