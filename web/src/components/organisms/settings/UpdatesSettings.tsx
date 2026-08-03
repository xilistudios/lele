import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthContext } from '../../../contexts/AuthContext'
import type { SystemVersionInfo, UpdateCheckInfo, UpdateState } from '../../../lib/types'
import { Badge } from '../../atoms/Badge'
import { Spinner } from '../../atoms/Spinner'
import { SettingsSection } from '../../molecules'

type UpdateStatus = 'idle' | 'checking' | 'available' | 'up-to-date' | 'updating' | 'done' | 'error'

export function UpdatesSettings() {
  const { t } = useTranslation()
  const { api } = useAuthContext()

  const [versionInfo, setVersionInfo] = useState<SystemVersionInfo | null>(null)
  const [checkInfo, setCheckInfo] = useState<UpdateCheckInfo | null>(null)
  const [updateState, setUpdateState] = useState<UpdateState | null>(null)
  const [status, setStatus] = useState<UpdateStatus>('idle')
  const [error, setError] = useState<string | null>(null)
  const [showChangelog, setShowChangelog] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [rollbackConfirmOpen, setRollbackConfirmOpen] = useState(false)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Load version info on mount.
  useEffect(() => {
    api
      .systemVersion()
      .then(setVersionInfo)
      .catch(() => setVersionInfo(null))
  }, [api])

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  useEffect(() => stopPolling, [stopPolling])

  const startPolling = useCallback(() => {
    stopPolling()
    pollRef.current = setInterval(async () => {
      try {
        const state = await api.updatesStatus()
        setUpdateState(state)
        if (state.phase === 'done') {
          setStatus('done')
          stopPolling()
        } else if (state.phase === 'failed') {
          setStatus('error')
          setError(state.error || 'Update failed')
          stopPolling()
        }
      } catch {
        // ignore transient polling errors
      }
    }, 1500)
  }, [api, stopPolling])

  const handleCheck = async () => {
    setStatus('checking')
    setError(null)
    try {
      const info = await api.updatesCheck()
      setCheckInfo(info)
      setStatus(info.update_available ? 'available' : 'up-to-date')
    } catch (err) {
      setStatus('error')
      setError(err instanceof Error ? err.message : 'Check failed')
    }
  }

  const handleApply = async () => {
    setConfirmOpen(false)
    setStatus('updating')
    setError(null)
    try {
      await api.updatesApply(undefined, true)
      startPolling()
    } catch (err) {
      setStatus('error')
      setError(err instanceof Error ? err.message : 'Update failed')
    }
  }

  const handleRollback = async () => {
    setRollbackConfirmOpen(false)
    setStatus('updating')
    setError(null)
    try {
      await api.updatesRollback()
      setStatus('done')
    } catch (err) {
      setStatus('error')
      setError(err instanceof Error ? err.message : 'Rollback failed')
    }
  }

  const isUpdating = status === 'updating'
  const progress = updateState?.phase === 'downloading' ? updateState.progress : null

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.updates.title')}
        description={t('settings.updates.description')}
      >
        {/* Current version info */}
        {versionInfo && (
          <div className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
            <span className="text-text-secondary">{t('settings.updates.version')}</span>
            <span className="font-mono text-text-primary">{versionInfo.version}</span>
            {versionInfo.git_commit && (
              <>
                <span className="text-text-secondary">{t('settings.updates.commit')}</span>
                <span className="font-mono text-text-primary">{versionInfo.git_commit}</span>
              </>
            )}
            {versionInfo.build_time && (
              <>
                <span className="text-text-secondary">{t('settings.updates.buildTime')}</span>
                <span className="font-mono text-text-primary">{versionInfo.build_time}</span>
              </>
            )}
            <span className="text-text-secondary">{t('settings.updates.platform')}</span>
            <span className="font-mono text-text-primary">
              {versionInfo.os}/{versionInfo.arch}
            </span>
            {versionInfo.binary && (
              <>
                <span className="text-text-secondary">{t('settings.updates.binary')}</span>
                <span className="font-mono text-text-primary break-all">{versionInfo.binary}</span>
              </>
            )}
          </div>
        )}

        {versionInfo?.dev_build && (
          <Badge variant="warning">{t('settings.updates.devBuild')}</Badge>
        )}

        {/* Status / progress */}
        {status === 'checking' && (
          <div className="flex items-center gap-2 text-sm text-text-secondary">
            <Spinner size="sm" />
            {t('settings.updates.checking')}
          </div>
        )}

        {status === 'up-to-date' && checkInfo && (
          <div className="text-sm text-text-secondary">
            ✓ {t('settings.updates.upToDate', { version: checkInfo.latest })}
          </div>
        )}

        {status === 'available' && checkInfo && (
          <div className="space-y-3 rounded-md border border-accent-primary/30 bg-accent-primary/5 p-4">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm font-semibold text-text-primary">
                  {t('settings.updates.updateAvailable', { version: checkInfo.latest })}
                </div>
                {checkInfo.published_at && (
                  <div className="text-xs text-text-secondary">
                    {new Date(checkInfo.published_at).toLocaleDateString()}
                  </div>
                )}
              </div>
              <button
                type="button"
                onClick={() => setConfirmOpen(true)}
                className="rounded-md bg-accent-primary px-4 py-2 text-xs font-semibold text-text-on-accent shadow-sm hover:bg-accent-primary/90 transition-colors"
              >
                {t('settings.updates.updateNow')}
              </button>
            </div>
            {checkInfo.changelog && (
              <div>
                <button
                  type="button"
                  onClick={() => setShowChangelog(!showChangelog)}
                  className="text-xs text-accent-primary hover:underline"
                >
                  {showChangelog
                    ? t('settings.updates.hideChangelog')
                    : t('settings.updates.showChangelog')}
                </button>
                {showChangelog && (
                  <pre className="mt-2 max-h-48 overflow-y-auto whitespace-pre-wrap rounded bg-background-secondary p-3 text-xs text-text-secondary">
                    {checkInfo.changelog}
                  </pre>
                )}
              </div>
            )}
          </div>
        )}

        {isUpdating && updateState && (
          <div className="space-y-2">
            <div className="flex items-center gap-2 text-sm text-text-secondary">
              <Spinner size="sm" />
              {t(`settings.updates.phase.${updateState.phase}`, {
                defaultValue: updateState.phase,
              })}
            </div>
            {progress !== null && (
              <div className="h-2 w-full overflow-hidden rounded-full bg-background-secondary">
                <div
                  className="h-full rounded-full bg-accent-primary transition-all duration-300"
                  style={{ width: `${Math.min(progress, 100)}%` }}
                />
              </div>
            )}
          </div>
        )}

        {status === 'done' && (
          <div className="text-sm text-green-600 dark:text-green-400">
            ✓ {t('settings.updates.completed')}
          </div>
        )}

        {error && (
          <div className="rounded-md border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-600 dark:text-red-400">
            {error}
          </div>
        )}

        {/* Actions */}
        <div className="flex gap-2 pt-2">
          <button
            type="button"
            onClick={handleCheck}
            disabled={isUpdating || status === 'checking'}
            className="rounded-md border border-border bg-background-secondary px-3 py-2 text-xs font-medium text-text-primary hover:bg-background-tertiary transition-colors disabled:opacity-50"
          >
            {t('settings.updates.checkForUpdates')}
          </button>
          {versionInfo?.has_backup && (
            <button
              type="button"
              onClick={() => setRollbackConfirmOpen(true)}
              disabled={isUpdating}
              className="rounded-md border border-border bg-background-secondary px-3 py-2 text-xs font-medium text-text-secondary hover:bg-background-tertiary transition-colors disabled:opacity-50"
            >
              {t('settings.updates.rollback')}
            </button>
          )}
        </div>
      </SettingsSection>

      {/* Confirmation modal */}
      {confirmOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-md rounded-lg border border-border bg-background-primary p-6 shadow-xl">
            <h3 className="mb-2 text-sm font-semibold text-text-primary">
              {t('settings.updates.confirmTitle')}
            </h3>
            <p className="mb-4 text-xs text-text-secondary">
              {t('settings.updates.confirmMessage', { version: checkInfo?.latest })}
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirmOpen(false)}
                className="rounded-md border border-border px-3 py-2 text-xs font-medium text-text-primary hover:bg-background-secondary"
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                onClick={handleApply}
                className="rounded-md bg-accent-primary px-3 py-2 text-xs font-semibold text-text-on-accent hover:bg-accent-primary/90"
              >
                {t('settings.updates.confirmUpdate')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Rollback confirmation modal */}
      {rollbackConfirmOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-md rounded-lg border border-border bg-background-primary p-6 shadow-xl">
            <h3 className="mb-2 text-sm font-semibold text-text-primary">
              {t('settings.updates.rollbackConfirmTitle')}
            </h3>
            <p className="mb-4 text-xs text-text-secondary">
              {t('settings.updates.rollbackConfirmMessage')}
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setRollbackConfirmOpen(false)}
                className="rounded-md border border-border px-3 py-2 text-xs font-medium text-text-primary hover:bg-background-secondary"
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                onClick={handleRollback}
                className="rounded-md bg-red-600 px-3 py-2 text-xs font-semibold text-white hover:bg-red-500"
              >
                {t('settings.updates.rollback')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
