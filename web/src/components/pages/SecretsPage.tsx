import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useSecrets } from '../../hooks/useSecrets'
import type { SecretAuditRecord, SecretInput, SecretMeta } from '../../lib/types'
import { Sidebar } from '../organisms/Sidebar'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTimestamp(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString()
}

function BackendBadge({ backend }: { backend?: string }) {
  if (!backend) return null
  const isKeychain = /keychain|keyring|kwallet|gnome|secret/i.test(backend)
  const color = isKeychain
    ? 'border-emerald-500/30 bg-emerald-500/20 text-emerald-400'
    : 'border-amber-500/30 bg-amber-500/20 text-amber-400'
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${color}`}
    >
      {backend}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Secret card
// ---------------------------------------------------------------------------

function SecretCard({
  secret,
  onReveal,
  onDelete,
  busy,
}: {
  secret: SecretMeta
  onReveal: (name: string) => Promise<string>
  onDelete: (name: string) => Promise<void>
  busy: boolean
}) {
  const { t } = useTranslation()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [revealed, setRevealed] = useState<string | null>(null)
  const [revealing, setRevealing] = useState(false)
  const [copied, setCopied] = useState(false)
  const [revealError, setRevealError] = useState<string | null>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (hideTimer.current) clearTimeout(hideTimer.current)
    }
  }, [])

  const handleReveal = useCallback(async () => {
    if (revealed !== null) {
      setRevealed(null)
      if (hideTimer.current) clearTimeout(hideTimer.current)
      return
    }
    setRevealing(true)
    setRevealError(null)
    try {
      const value = await onReveal(secret.name)
      setRevealed(value)
      // Auto-hide after 10 seconds
      hideTimer.current = setTimeout(() => setRevealed(null), 10_000)
    } catch (err) {
      setRevealError((err as Error).message)
    } finally {
      setRevealing(false)
    }
  }, [revealed, onReveal, secret.name])

  const handleCopy = useCallback(async () => {
    if (revealed === null) return
    try {
      await navigator.clipboard.writeText(revealed)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // clipboard unavailable — ignore
    }
  }, [revealed])

  const tags = secret.tags ?? []
  const scope = secret.scope ?? []

  return (
    <div className="rounded-xl border border-border bg-background-secondary/50 transition-colors hover:bg-background-secondary">
      <div className="flex items-start gap-4 px-4 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm font-semibold text-text-primary">{secret.name}</span>
            {scope.length === 0 ? (
              <span className="inline-flex items-center rounded-full border border-gray-500/30 bg-gray-500/20 px-2 py-0.5 text-xs text-gray-400">
                {t('secrets.allAgents', 'all agents')}
              </span>
            ) : (
              scope.map((id) => (
                <span
                  key={id}
                  className="inline-flex items-center rounded-full border border-interaction-primary/30 bg-interaction-primary/15 px-2 py-0.5 text-xs text-interaction-primary"
                >
                  {id}
                </span>
              ))
            )}
          </div>
          {secret.description && (
            <p className="mt-1 text-sm text-text-secondary">{secret.description}</p>
          )}
          {tags.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {tags.map((tag) => (
                <span
                  key={tag}
                  className="rounded bg-background-tertiary px-1.5 py-0.5 text-xs text-text-tertiary"
                >
                  #{tag}
                </span>
              ))}
            </div>
          )}

          {revealed !== null && (
            <div className="mt-3 flex items-center gap-2 rounded-lg border border-border bg-background-primary p-2">
              <code className="flex-1 break-all font-mono text-xs text-text-primary">
                {revealed}
              </code>
              <button
                type="button"
                onClick={handleCopy}
                className="shrink-0 rounded-md border border-border px-2 py-1 text-xs text-text-secondary transition-colors hover:bg-surface-hover"
              >
                {copied ? t('secrets.copied', 'Copied!') : t('secrets.copy', 'Copy')}
              </button>
            </div>
          )}
          {revealError && <p className="mt-2 text-xs text-red-400">{revealError}</p>}

          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-tertiary">
            <span>
              {t('secrets.createdBy', 'Created by')} {secret.created_by || '—'}
            </span>
            <span>
              {t('secrets.updatedAt', 'Updated')} {formatTimestamp(secret.updated_at)}
            </span>
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={handleReveal}
            disabled={revealing || busy}
            className="rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
          >
            {revealing
              ? '…'
              : revealed !== null
                ? t('secrets.hide', 'Hide')
                : t('secrets.reveal', 'Reveal')}
          </button>
          {confirmDelete ? (
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => onDelete(secret.name)}
                disabled={busy}
                className="rounded-lg bg-red-500/90 px-2.5 py-1.5 text-xs font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-50"
              >
                {t('common.confirm', 'Confirm')}
              </button>
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                disabled={busy}
                className="rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-text-secondary transition-colors hover:bg-surface-hover"
              >
                {t('common.cancel', 'Cancel')}
              </button>
            </div>
          ) : (
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              disabled={busy}
              className="rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-red-400 transition-colors hover:bg-red-500/10 disabled:opacity-50"
            >
              {t('secrets.delete', 'Delete')}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Add / edit form modal
// ---------------------------------------------------------------------------

function SecretFormModal({
  busy,
  onClose,
  onSubmit,
}: {
  busy: boolean
  onClose: () => void
  onSubmit: (input: SecretInput) => Promise<void>
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<SecretInput>({
    name: '',
    value: '',
    description: '',
    tags: [],
    scope: [],
  })
  const [tagsText, setTagsText] = useState('')
  const [scopeText, setScopeText] = useState('')
  const [showValue, setShowValue] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = useCallback(async () => {
    if (!form.name.trim()) {
      setError(t('secrets.errorName', 'Name is required'))
      return
    }
    if (!form.value.trim()) {
      setError(t('secrets.errorValue', 'Value is required'))
      return
    }
    setError(null)
    const input: SecretInput = {
      name: form.name.trim(),
      value: form.value,
      description: form.description?.trim() || undefined,
      tags: tagsText
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean),
      scope: scopeText
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean),
    }
    try {
      await onSubmit(input)
      onClose()
    } catch (err) {
      setError((err as Error).message)
    }
  }, [form, tagsText, scopeText, onSubmit, onClose, t])

  const labelCls = 'mb-1 block text-xs font-medium text-text-secondary'
  const inputCls =
    'w-full rounded-lg border border-border bg-background-primary px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:border-interaction-primary focus:outline-none'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <dialog
        open
        className="w-full max-w-lg rounded-2xl border border-border bg-background-secondary p-6 shadow-2xl"
      >
        <h2 className="mb-4 text-base font-semibold text-text-primary">
          {t('secrets.newSecret', 'New Secret')}
        </h2>

        <div className="space-y-4">
          <div>
            <label className={labelCls} htmlFor="secret-name">
              {t('secrets.name', 'Name')}
            </label>
            <input
              id="secret-name"
              className={`${inputCls} font-mono`}
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder={t('secrets.namePlaceholder', 'e.g. openai.api_key')}
            />
          </div>

          <div>
            <label className={labelCls} htmlFor="secret-value">
              {t('secrets.value', 'Value')}
            </label>
            <div className="relative">
              <input
                id="secret-value"
                type={showValue ? 'text' : 'password'}
                className={`${inputCls} pr-16`}
                value={form.value}
                onChange={(e) => setForm((f) => ({ ...f, value: e.target.value }))}
                placeholder={t('secrets.valuePlaceholder', 'Secret value (stored encrypted)')}
              />
              <button
                type="button"
                onClick={() => setShowValue((s) => !s)}
                className="absolute right-2 top-1/2 -translate-y-1/2 rounded px-2 py-1 text-xs text-text-tertiary transition-colors hover:text-text-primary"
              >
                {showValue ? t('secrets.hide', 'Hide') : t('secrets.reveal', 'Reveal')}
              </button>
            </div>
          </div>

          <div>
            <label className={labelCls} htmlFor="secret-desc">
              {t('secrets.description', 'Description')} ({t('common.optional', 'optional')})
            </label>
            <input
              id="secret-desc"
              className={inputCls}
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder={t('secrets.descriptionPlaceholder', 'Optional description')}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={labelCls} htmlFor="secret-tags">
                {t('secrets.tags', 'Tags')}
              </label>
              <input
                id="secret-tags"
                className={inputCls}
                value={tagsText}
                onChange={(e) => setTagsText(e.target.value)}
                placeholder={t('secrets.tagsPlaceholder', 'Comma-separated tags')}
              />
            </div>
            <div>
              <label className={labelCls} htmlFor="secret-scope">
                {t('secrets.scope', 'Scope')}
              </label>
              <input
                id="secret-scope"
                className={inputCls}
                value={scopeText}
                onChange={(e) => setScopeText(e.target.value)}
                placeholder={t('secrets.scopePlaceholder', 'Agent IDs (empty = all agents)')}
              />
            </div>
          </div>

          {error && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-400">
              {error}
            </div>
          )}
        </div>

        <div className="mt-6 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-border bg-background-secondary px-4 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover"
          >
            {t('common.cancel', 'Cancel')}
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            disabled={busy}
            className="rounded-lg bg-interaction-primary px-4 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90 disabled:opacity-50"
          >
            {t('common.create', 'Create')}
          </button>
        </div>
      </dialog>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

function AuditLog({ records }: { records: SecretAuditRecord[] }) {
  const { t } = useTranslation()
  if (records.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-text-tertiary">
        {t('secrets.auditEmpty', 'No access records')}
      </p>
    )
  }
  return (
    <div className="overflow-x-auto rounded-xl border border-border">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-border bg-background-secondary/50 text-xs uppercase text-text-tertiary">
          <tr>
            <th className="px-3 py-2 font-medium">{t('secrets.name', 'Name')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.action', 'Action')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.agent', 'Agent')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.granted', 'Granted')}</th>
            <th className="px-3 py-2 font-medium">Time</th>
          </tr>
        </thead>
        <tbody>
          {records.map((r, i) => (
            <tr key={`${r.timestamp}-${i}`} className="border-b border-border/50 last:border-0">
              <td className="px-3 py-2 font-mono text-text-primary">{r.secret_name}</td>
              <td className="px-3 py-2 text-text-secondary">{r.action}</td>
              <td className="px-3 py-2 text-text-secondary">{r.agent_id || '—'}</td>
              <td className="px-3 py-2">
                {r.granted ? (
                  <span className="text-emerald-400">{t('secrets.granted', 'Granted')}</span>
                ) : (
                  <span className="text-red-400">{t('secrets.denied', 'Denied')}</span>
                )}
              </td>
              <td className="px-3 py-2 text-xs text-text-tertiary">
                {formatTimestamp(r.timestamp)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export function SecretsPage() {
  const { t } = useTranslation()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()
  const {
    secrets,
    status,
    audit,
    loading,
    refresh,
    refreshAudit,
    reveal,
    createSecret,
    removeSecret,
  } = useSecrets()
  const [formOpen, setFormOpen] = useState(false)
  const [formBusy, setFormBusy] = useState(false)
  const [busyName, setBusyName] = useState<string | null>(null)
  const [tab, setTab] = useState<'secrets' | 'audit'>('secrets')

  const openCreate = useCallback(() => setFormOpen(true), [])

  const handleCreate = useCallback(
    async (input: SecretInput) => {
      setFormBusy(true)
      try {
        await createSecret(input)
      } finally {
        setFormBusy(false)
      }
    },
    [createSecret],
  )

  const handleDelete = useCallback(
    async (name: string) => {
      setBusyName(name)
      try {
        await removeSecret(name)
        await refreshAudit()
      } finally {
        setBusyName(null)
      }
    },
    [removeSecret, refreshAudit],
  )

  const handleRefresh = useCallback(async () => {
    await refresh()
    await refreshAudit()
  }, [refresh, refreshAudit])

  const tabCls = (active: boolean) =>
    `rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
      active
        ? 'bg-interaction-primary text-white'
        : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
    }`

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <header className="flex shrink-0 items-center justify-between border-b border-border bg-background-secondary/30 px-6 py-4">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onToggleSidebar()}
              className="flex rounded-lg p-1.5 text-text-tertiary transition-colors hover:bg-background-tertiary hover:text-text-primary md:hidden"
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
              {t('secrets.title', 'Secrets')}
            </h1>
            {status && (
              <span className="flex items-center gap-2 text-xs text-text-tertiary">
                <BackendBadge backend={status.backend} />
                {status.count} {t('secrets.count', 'secrets')}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <div className="flex items-center gap-1 rounded-lg border border-border p-0.5">
              <button
                type="button"
                className={tabCls(tab === 'secrets')}
                onClick={() => setTab('secrets')}
              >
                {t('secrets.title', 'Secrets')}
              </button>
              <button
                type="button"
                className={tabCls(tab === 'audit')}
                onClick={() => setTab('audit')}
              >
                {t('secrets.audit', 'Audit Log')}
              </button>
            </div>
            {tab === 'secrets' && (
              <button
                type="button"
                onClick={openCreate}
                className="inline-flex items-center gap-1.5 rounded-lg bg-interaction-primary px-3 py-2 text-sm font-medium text-white transition-opacity hover:opacity-90"
              >
                <svg
                  className="h-4 w-4"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                >
                  <title>Add</title>
                  <line x1="12" y1="5" x2="12" y2="19" />
                  <line x1="5" y1="12" x2="19" y2="12" />
                </svg>
                {t('secrets.new', 'New')}
              </button>
            )}
            <button
              type="button"
              onClick={handleRefresh}
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
          </div>
        </header>

        <div className="flex-1 overflow-y-auto p-6">
          {tab === 'audit' ? (
            <AuditLog records={audit} />
          ) : (
            <>
              {loading && secrets.length === 0 && (
                <div className="flex items-center justify-center py-20">
                  <div className="h-8 w-8 animate-spin rounded-full border-2 border-interaction-primary border-t-transparent" />
                </div>
              )}

              {!loading && secrets.length === 0 && (
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
                    <title>Lock</title>
                    <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                    <path d="M7 11V7a5 5 0 0 1 10 0v4" />
                  </svg>
                  <p className="text-sm text-text-tertiary">
                    {t('secrets.empty', 'No secrets stored')}
                  </p>
                  <button
                    type="button"
                    onClick={openCreate}
                    className="mt-4 rounded-lg border border-border bg-background-secondary px-4 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover"
                  >
                    {t('secrets.createFirst', 'Add your first secret')}
                  </button>
                </div>
              )}

              {secrets.length > 0 && (
                <div className="space-y-2">
                  {secrets.map((secret) => (
                    <SecretCard
                      key={secret.name}
                      secret={secret}
                      onReveal={reveal}
                      onDelete={handleDelete}
                      busy={busyName === secret.name}
                    />
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      </main>

      {formOpen && (
        <SecretFormModal
          busy={formBusy}
          onClose={() => setFormOpen(false)}
          onSubmit={handleCreate}
        />
      )}
    </div>
  )
}
