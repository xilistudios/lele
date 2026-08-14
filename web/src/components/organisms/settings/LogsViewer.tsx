import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthContext } from '../../../contexts/AuthContext'
import type { LogEntry } from '../../../lib/types'

export function LogsViewer() {
  const { t } = useTranslation()
  const { api } = useAuthContext()

  const [dates, setDates] = useState<string[]>([])
  const [selectedDate, setSelectedDate] = useState(() => new Date().toISOString().slice(0, 10))
  const [selectedLevel, setSelectedLevel] = useState<'info' | 'error'>('info')
  const [lines, setLines] = useState(200)
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [meta, setMeta] = useState<{
    total_lines: number
    returned_lines: number
    file: string
  } | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  // Fetch available dates on mount
  useEffect(() => {
    api.logs
      .dates()
      .then((res) => setDates(res.dates))
      .catch(() => {})
  }, [api])

  // Fetch logs when params change
  const fetchLogs = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.logs.list({ level: selectedLevel, date: selectedDate, lines })
      setEntries(res.entries)
      setMeta({ total_lines: res.total_lines, returned_lines: res.returned_lines, file: res.file })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load logs')
    } finally {
      setLoading(false)
    }
  }, [api, selectedLevel, selectedDate, lines])

  useEffect(() => {
    fetchLogs()
  }, [fetchLogs])

  // Auto-scroll to bottom
  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight
    }
  }, [entries])

  const levelColor = (level: string) => {
    switch (level) {
      case 'ERROR':
      case 'FATAL':
        return 'text-red-400'
      case 'WARN':
        return 'text-yellow-400'
      case 'DEBUG':
        return 'text-gray-400'
      default:
        return 'text-blue-400'
    }
  }

  return (
    <div className="space-y-4">
      {/* Controls */}
      <div className="flex flex-wrap items-center gap-3">
        <select
          value={selectedDate}
          onChange={(e) => setSelectedDate(e.target.value)}
          className="rounded-md border border-border bg-background-secondary px-2 py-1.5 text-xs text-text-primary"
        >
          {dates.length === 0 && <option value={selectedDate}>{selectedDate}</option>}
          {dates.map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
        </select>

        <select
          value={selectedLevel}
          onChange={(e) => setSelectedLevel(e.target.value as 'info' | 'error')}
          className="rounded-md border border-border bg-background-secondary px-2 py-1.5 text-xs text-text-primary"
        >
          <option value="info">{t('settings.logs.levelInfo')}</option>
          <option value="error">{t('settings.logs.levelError')}</option>
        </select>

        <select
          value={lines}
          onChange={(e) => setLines(Number(e.target.value))}
          className="rounded-md border border-border bg-background-secondary px-2 py-1.5 text-xs text-text-primary"
        >
          <option value={100}>100</option>
          <option value={200}>200</option>
          <option value={500}>500</option>
        </select>

        <button
          type="button"
          onClick={fetchLogs}
          disabled={loading}
          className="rounded-md border border-border bg-background-secondary px-3 py-1.5 text-xs text-text-primary hover:bg-surface-hover disabled:opacity-50"
        >
          {loading ? t('common.loading') : t('settings.logs.refresh')}
        </button>

        {meta && (
          <span className="text-[10px] text-text-tertiary">
            {t('settings.logs.showing', { returned: meta.returned_lines, total: meta.total_lines })}{' '}
            — {meta.file}
          </span>
        )}
      </div>

      {/* Error */}
      {error && (
        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">
          {error}
        </div>
      )}

      {/* Log entries */}
      <div className="rounded-lg border border-border bg-background-secondary overflow-hidden">
        <div
          ref={containerRef}
          className="max-h-[600px] overflow-y-auto p-3 font-mono text-[11px] leading-5"
        >
          {entries.length === 0 && !loading && (
            <p className="text-text-tertiary">{t('settings.logs.empty')}</p>
          )}
          {entries.map((entry, i) => (
            <div key={i} className="flex gap-2 whitespace-pre-wrap break-all">
              <span className="shrink-0 text-text-tertiary">
                {entry.timestamp?.slice(11, 19) ?? ''}
              </span>
              <span className={`shrink-0 font-semibold ${levelColor(entry.level)}`}>
                {entry.level}
              </span>
              {entry.component && (
                <span className="shrink-0 text-text-tertiary">[{entry.component}]</span>
              )}
              <span className="text-text-primary">{entry.message}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
