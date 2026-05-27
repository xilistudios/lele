import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SubagentTaskInfo } from '../../lib/types'
import { CloseIcon } from '../atoms/Icons'
import { IconButton } from '../atoms/IconButton'
import { Spinner } from '../atoms/Spinner'

interface SubagentsSidebarProps {
  subagents: SubagentTaskInfo[]
  loading: boolean
  isOpen: boolean
  onClose: () => void
  onSelectSubagent: (sessionKey: string) => void
}

const ANIMATION_DURATION_MS = 300

export function SubagentsSidebar({
  subagents,
  loading,
  isOpen,
  onClose,
  onSelectSubagent,
}: SubagentsSidebarProps) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)
  const [animate, setAnimate] = useState(false)

  const rafRef = useRef<number>(0)

  useEffect(() => {
    if (isOpen) {
      setVisible(true)
      // Use double-rAF via useLayoutEffect timing to ensure the browser has
      // painted the initial off-screen state before we trigger the transition.
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = requestAnimationFrame(() => setAnimate(true))
      })
      return () => cancelAnimationFrame(rafRef.current)
    } else {
      setAnimate(false)
      const timer = setTimeout(() => setVisible(false), ANIMATION_DURATION_MS)
      return () => {
        clearTimeout(timer)
        cancelAnimationFrame(rafRef.current)
      }
    }
  }, [isOpen])

  const handleClick = useCallback(
    (sessionKey: string) => {
      onSelectSubagent(sessionKey)
      onClose()
    },
    [onSelectSubagent, onClose],
  )

  if (!visible) {
    return null
  }

  return (
    <>
      {/* Backdrop — fades in/out */}
      <button
        type="button"
        className={`fixed inset-0 z-40 bg-black/40 transition-opacity duration-300 ease-out ${animate ? 'opacity-100' : 'opacity-0 pointer-events-none'}`}
        onClick={onClose}
        aria-label={t('common.close')}
      />

      {/* Sidebar — slides in from right */}
      <div className={`fixed right-0 top-0 z-50 h-full w-80 bg-background-primary border-l border-border shadow-lg flex flex-col transition-transform duration-300 ease-out ${animate ? 'translate-x-0' : 'translate-x-full'}`}>
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h3 className="text-sm font-medium text-text-primary">{t('chat.subagentsTitle')}</h3>
          <IconButton
            onClick={onClose}
            className="rounded p-1 text-text-tertiary hover:bg-surface-hover hover:text-text-secondary transition-colors"
            aria-label={t('common.close')}
          >
            <CloseIcon size={16} />
          </IconButton>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {loading && subagents.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full text-text-tertiary gap-2">
              <Spinner size="md" />
              <p className="text-sm">{t('common.loading')}</p>
            </div>
          ) : subagents.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full text-text-tertiary">
              <p className="text-sm">{t('chat.noSubagents')}</p>
            </div>
          ) : (
            <ul className="divide-y divide-border">
              {subagents.map((subagent) => (
                <li key={subagent.task_id}>
                  <button
                    type="button"
                    onClick={() => handleClick(subagent.session_key)}
                    className="w-full px-4 py-3 text-left hover:bg-surface-hover transition-colors"
                  >
                    <div className="flex items-center justify-between">
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-text-primary truncate">
                          {subagent.label || subagent.task_id}
                        </p>
                        <div className="flex items-center gap-2 mt-0.5">
                          <StatusBadge status={subagent.status} />
                          {subagent.summary && (
                            <p className="text-xs text-text-tertiary truncate">
                              {subagent.summary}
                            </p>
                          )}
                        </div>
                        <p className="text-[11px] text-text-tertiary mt-0.5">
                          {formatRelativeTime(subagent.created)}
                          {subagent.iterations > 0 && ` · ${subagent.iterations} iter`}
                        </p>
                      </div>
                      {subagent.status === 'running' && (
                        <div className="ml-2 flex-shrink-0">
                          <Spinner size="sm" />
                        </div>
                      )}
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </>
  )
}

function StatusBadge({ status }: { status: string }) {
  const colorClass = getStatusColor(status)
  return (
    <span
      className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${colorClass}`}
    >
      {status}
    </span>
  )
}

function getStatusColor(status: string): string {
  switch (status) {
    case 'running':
      return 'bg-state-info/15 text-state-info'
    case 'completed':
      return 'bg-state-success/15 text-state-success'
    case 'failed':
    case 'cancelled':
      return 'bg-state-error/15 text-state-error'
    case 'needs_context':
      return 'bg-state-warning/15 text-state-warning'
    case 'not_done':
      return 'bg-state-warning/15 text-state-warning'
    default:
      return 'bg-surface-card text-text-tertiary'
  }
}

function formatRelativeTime(ms: number): string {
  const date = new Date(ms)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSeconds = Math.floor(diffMs / 1000)
  const diffMinutes = Math.floor(diffSeconds / 60)
  const diffHours = Math.floor(diffMinutes / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffSeconds < 60) {
    return 'just now'
  }
  if (diffMinutes < 60) {
    return `${diffMinutes}m ago`
  }
  if (diffHours < 24) {
    return `${diffHours}h ago`
  }
  if (diffDays < 7) {
    return `${diffDays}d ago`
  }
  return date.toLocaleDateString()
}
