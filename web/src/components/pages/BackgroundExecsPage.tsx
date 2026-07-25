import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useBackgroundExecStream } from '../../hooks/useBackgroundExecStream'
import { useBackgroundExecs } from '../../hooks/useBackgroundExecs'
import type { BackgroundExecInfo } from '../../lib/types'
import { Sidebar } from '../organisms/Sidebar'

function formatElapsed(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remaining = seconds % 60
  if (minutes < 60) return `${minutes}m ${remaining}s`
  const hours = Math.floor(minutes / 60)
  const remainingMin = minutes % 60
  return `${hours}h ${remainingMin}m`
}

function StatusBadge({ status }: { status: BackgroundExecInfo['status'] }) {
  const colors: Record<string, string> = {
    running: 'bg-amber-500/20 text-amber-400 border-amber-500/30',
    completed: 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30',
    stopped: 'bg-gray-500/20 text-gray-400 border-gray-500/30',
    failed: 'bg-red-500/20 text-red-400 border-red-500/30',
  }

  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${colors[status] ?? colors.stopped}`}
    >
      {status === 'running' && (
        <span className="mr-1.5 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" />
      )}
      {status}
    </span>
  )
}

function ProcessOutput({ processId }: { processId: string }) {
  const { t } = useTranslation()
  const { output, status, elapsedMs, done } = useBackgroundExecStream(processId)
  const outputRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight
    }
  }, [output])

  return (
    <div className="mt-3 space-y-2">
      <div className="flex items-center gap-4 text-xs text-text-secondary">
        <span>
          {t('backgroundExecs.status')}:{' '}
          <StatusBadge status={status as BackgroundExecInfo['status']} />
        </span>
        <span>
          {t('backgroundExecs.elapsed')}: {formatElapsed(elapsedMs)}
        </span>
        {done && <span className="text-emerald-400">✓</span>}
      </div>
      <div
        ref={outputRef}
        className="max-h-80 overflow-y-auto rounded-lg border border-border bg-gray-950 p-3 font-mono text-xs text-gray-300 whitespace-pre-wrap break-all"
      >
        {output || (
          <span className="text-text-tertiary italic">{t('common.loading', 'Loading...')}</span>
        )}
      </div>
    </div>
  )
}

function ProcessCard({
  process: proc,
  expanded,
  onToggle,
  onStop,
}: {
  process: BackgroundExecInfo
  expanded: boolean
  onToggle: () => void
  onStop: () => void
}) {
  const { t } = useTranslation()

  return (
    <div
      className={`rounded-xl border transition-colors ${
        expanded
          ? 'border-interaction-primary/40 bg-background-secondary'
          : 'border-border bg-background-secondary/50 hover:bg-background-secondary'
      }`}
    >
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center gap-4 px-4 py-3 text-left"
      >
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate font-mono text-sm text-text-primary">{proc.command}</span>
              <StatusBadge status={proc.status} />
            </div>
            <div className="mt-1 flex items-center gap-3 text-xs text-text-tertiary">
              <span>
                {t('backgroundExecs.agent')}: {proc.agent_id}
              </span>
              {proc.working_dir && <span className="truncate">📁 {proc.working_dir}</span>}
              <span>{formatElapsed(proc.elapsed_ms)}</span>
              <span className="font-mono text-text-tertiary/60">{proc.id.slice(0, 8)}</span>
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {proc.status === 'running' && (
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                onStop()
              }}
              className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1 text-xs font-medium text-red-400 transition-colors hover:bg-red-500/20"
            >
              {t('backgroundExecs.stop', 'Stop')}
            </button>
          )}
          <svg
            className={`h-4 w-4 text-text-tertiary transition-transform ${expanded ? 'rotate-180' : ''}`}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <title>
              {expanded ? t('backgroundExecs.hideOutput') : t('backgroundExecs.viewOutput')}
            </title>
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </div>
      </button>
      {expanded && (
        <div className="border-t border-border px-4 pb-4 pt-2">
          <ProcessOutput processId={proc.id} />
        </div>
      )}
    </div>
  )
}

export function BackgroundExecsPage() {
  const { t } = useTranslation()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()
  const { processes, loading, refresh, stopProcess } = useBackgroundExecs()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const handleToggle = useCallback((id: string) => {
    setExpandedId((prev) => (prev === id ? null : id))
  }, [])

  const handleStop = useCallback(
    (id: string) => {
      stopProcess(id)
    },
    [stopProcess],
  )

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        {/* Header */}
        <header className="flex items-center justify-between border-b border-border px-6 py-4 shrink-0 bg-background-secondary/30">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onToggleSidebar()}
              className="flex md:hidden p-1.5 rounded-lg text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-colors"
              title={t('chat.toggleSidebar')}
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>Toggle sidebar</title>
                <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
                <line x1="9" y1="3" x2="9" y2="21" />
              </svg>
            </button>
            <h1 className="text-base font-semibold text-text-primary">
              {t('backgroundExecs.title', 'Background Processes')}
            </h1>
          </div>
          <button
            type="button"
            onClick={refresh}
            disabled={loading}
            className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-background-secondary px-3 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
          >
            <svg
              className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`}
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <title>Refresh</title>
              <path d="M21 12a9 9 0 1 1-6.219-8.56" />
            </svg>
            {t('common.refresh', 'Refresh')}
          </button>
        </header>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6">
          {loading && processes.length === 0 && (
            <div className="flex items-center justify-center py-20">
              <div className="h-8 w-8 animate-spin rounded-full border-2 border-interaction-primary border-t-transparent" />
            </div>
          )}

          {!loading && processes.length === 0 && (
            <div className="flex flex-col items-center justify-center py-20 text-center">
              <svg
                className="mb-4 h-12 w-12 text-text-tertiary/40"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
                <line x1="8" y1="21" x2="16" y2="21" />
                <line x1="12" y1="17" x2="12" y2="21" />
              </svg>
              <p className="text-sm text-text-tertiary">
                {t('backgroundExecs.empty', 'No background processes')}
              </p>
            </div>
          )}

          {processes.length > 0 && (
            <div className="space-y-2">
              {processes.map((proc) => (
                <ProcessCard
                  key={proc.id}
                  process={proc}
                  expanded={expandedId === proc.id}
                  onToggle={() => handleToggle(proc.id)}
                  onStop={() => handleStop(proc.id)}
                />
              ))}
            </div>
          )}
        </div>
      </main>
    </div>
  )
}
