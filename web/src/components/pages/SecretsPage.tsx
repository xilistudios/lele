import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useSecrets } from '../../hooks/useSecrets'
import type { Agent, SecretAuditRecord, SecretInput, SecretMeta } from '../../lib/types'
import { BackButton, Button, ErrorBanner, Modal } from '../atoms'
import { AgentChipMultiSelect } from '../molecules'
import { Sidebar } from '../organisms/Sidebar'

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
    ? 'border-state-success bg-state-success/10 text-state-success'
    : 'border-state-warning bg-state-warning/10 text-state-warning'
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${color}`}
    >
      {backend}
    </span>
  )
}

const CARD_ACTION_CLS =
  'flex-1 whitespace-nowrap rounded-lg border border-border px-2.5 py-2 text-xs font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-40 sm:flex-none sm:py-1.5'

const CARD_DANGER_CLS =
  'flex-1 whitespace-nowrap rounded-lg border border-border px-2.5 py-2 text-xs font-medium text-state-error transition-colors hover:bg-state-error/10 disabled:opacity-40 sm:flex-none sm:py-1.5'

const CARD_CONFIRM_CLS =
  'flex-1 whitespace-nowrap rounded-lg bg-state-error-fill px-2.5 py-2 text-xs font-medium text-text-on-accent transition-opacity hover:opacity-90 disabled:opacity-40 sm:flex-none sm:py-1.5'

function SecretCard({
  secret,
  onReveal,
  onEdit,
  onDelete,
  busy,
}: {
  secret: SecretMeta
  onReveal: (name: string) => Promise<string>
  onEdit: (secret: SecretMeta) => void
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
    } catch {}
  }, [revealed])

  const tags = secret.tags ?? []
  const scope = secret.scope ?? []

  return (
    <div className="rounded-xl border border-border bg-background-secondary transition-colors hover:bg-background-secondary">
      <div className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-start sm:gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 break-all font-mono text-sm font-semibold text-text-primary">
              {secret.name}
            </span>
            {scope.length === 0 ? (
              <span className="inline-flex items-center rounded-full border border-state-error bg-background-tertiary px-2 py-0.5 text-xs text-text-tertiary">
                {t('secrets.allAgents', 'all agents')}
              </span>
            ) : (
              scope.map((id) => (
                <span
                  key={id}
                  className="inline-flex items-center rounded-full border border-focus/30 bg-focus/15 px-2 py-0.5 text-xs text-focus"
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
          {revealError && <p className="mt-2 text-xs text-state-error">{revealError}</p>}

          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-tertiary">
            <span>
              {t('secrets.createdBy', 'Created by')} {secret.created_by || '—'}
            </span>
            <span>
              {t('secrets.updatedAt', 'Updated')} {formatTimestamp(secret.updated_at)}
            </span>
          </div>
        </div>

        <div className="flex items-center gap-1 sm:shrink-0">
          <button
            type="button"
            onClick={() => onEdit(secret)}
            disabled={busy}
            className={CARD_ACTION_CLS}
          >
            {t('common.edit', 'Edit')}
          </button>
          <button
            type="button"
            onClick={handleReveal}
            disabled={revealing || busy}
            className={CARD_ACTION_CLS}
          >
            {revealing
              ? '…'
              : revealed !== null
                ? t('secrets.hide', 'Hide')
                : t('secrets.reveal', 'Reveal')}
          </button>
          {confirmDelete ? (
            <div className="flex flex-1 items-center gap-1 sm:flex-none">
              <button
                type="button"
                onClick={() => onDelete(secret.name)}
                disabled={busy}
                className={CARD_CONFIRM_CLS}
              >
                {t('common.confirm', 'Confirm')}
              </button>
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                disabled={busy}
                className={CARD_ACTION_CLS}
              >
                {t('common.cancel', 'Cancel')}
              </button>
            </div>
          ) : (
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              disabled={busy}
              className={CARD_DANGER_CLS}
            >
              {t('secrets.delete', 'Delete')}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

export function SecretFormModal({
  busy,
  agents,
  initial,
  onClose,
  onSubmit,
}: {
  busy: boolean
  agents: Agent[]
  initial?: SecretMeta | null
  onClose: () => void
  onSubmit: (input: SecretInput) => Promise<void>
}) {
  const { t } = useTranslation()
  const isEdit = initial != null
  const [form, setForm] = useState<SecretInput>(() => ({
    name: initial?.name ?? '',
    value: '',
    description: initial?.description ?? '',
    tags: initial?.tags ?? [],
    scope: initial?.scope ?? [],
  }))
  const [tagsText, setTagsText] = useState(() => (initial?.tags ?? []).join(', '))
  const [showValue, setShowValue] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = useCallback(async () => {
    if (!form.name.trim()) {
      setError(t('secrets.errorName', 'Name is required'))
      return
    }
    if (!isEdit && !form.value.trim()) {
      setError(t('secrets.errorValue', 'Value is required'))
      return
    }
    setError(null)
    const input: SecretInput = {
      name: form.name.trim(),
      value: isEdit && !form.value.trim() ? '' : form.value,
      description: form.description?.trim() || undefined,
      tags: tagsText
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean),
      scope: form.scope ?? [],
    }
    try {
      await onSubmit(input)
      onClose()
    } catch (err) {
      setError((err as Error).message)
    }
  }, [form, tagsText, isEdit, onSubmit, onClose, t])

  const labelCls = 'mb-1 block text-xs font-medium text-text-secondary'
  const inputCls =
    'w-full rounded-lg border border-border bg-background-primary px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:border-focus focus:outline-none'

  return (
    <Modal
      isOpen
      onClose={onClose}
      title={isEdit ? t('secrets.editSecret', 'Edit Secret') : t('secrets.newSecret', 'New Secret')}
    >
      <div className="space-y-4 p-6">
        <div>
          <label className={labelCls} htmlFor="secret-name">
            {t('secrets.name', 'Name')}
          </label>
          <input
            id="secret-name"
            className={`${inputCls} font-mono disabled:cursor-not-allowed disabled:opacity-60`}
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            placeholder={t('secrets.namePlaceholder', 'e.g. openai.api_key')}
            disabled={isEdit}
          />
          {isEdit && (
            <p className="mt-1 text-xs text-text-tertiary">
              {t('secrets.nameImmutable', "The name is the secret's key and cannot be changed")}
            </p>
          )}
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
              placeholder={
                isEdit
                  ? t('secrets.valueKeepPlaceholder', 'Leave blank to keep the current value')
                  : t('secrets.valuePlaceholder', 'Secret value (stored encrypted)')
              }
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
          <span className={labelCls}>{t('secrets.scope', 'Scope')}</span>
          <p className="mb-2 text-xs text-text-tertiary">
            {t('secrets.scopeHelp', 'Empty = every agent can read this secret')}
          </p>
          <AgentChipMultiSelect
            id="secret-scope"
            agents={agents}
            value={form.scope ?? []}
            onChange={(value) => setForm((f) => ({ ...f, scope: value }))}
            disabled={busy}
            empty={t('secrets.noAgents', 'No agents configured')}
            ariaLabel={t('secrets.scope', 'Scope')}
          />
        </div>

        {error && (
          <div className="rounded-lg border border-state-error bg-state-error/10 p-2 text-xs text-state-error">
            {error}
          </div>
        )}
      </div>

      <div className="sticky bottom-0 flex justify-end gap-2 border-t border-border bg-background-primary px-6 py-4">
        <button
          type="button"
          onClick={onClose}
          className="rounded-lg border border-border bg-background-secondary px-4 py-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover"
        >
          {t('common.cancel', 'Cancel')}
        </button>
        <Button type="button" variant="primary" onClick={handleSubmit} disabled={busy}>
          {isEdit ? t('common.save', 'Save') : t('common.create', 'Create')}
        </Button>
      </div>
    </Modal>
  )
}

function AuditCell({
  label,
  className = '',
  children,
}: {
  label: string
  className?: string
  children: ReactNode
}) {
  return (
    <td className="flex items-baseline justify-between gap-3 py-0.5 md:table-cell md:px-3 md:py-2">
      <span className="shrink-0 text-xs uppercase text-text-tertiary md:hidden">{label}</span>
      <span className={`min-w-0 break-all text-right md:break-normal md:text-left ${className}`}>
        {children}
      </span>
    </td>
  )
}

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
    <div className="md:overflow-x-auto rounded-xl border border-border">
      <table className="block w-full text-left text-sm md:table">
        <thead className="hidden border-b border-border bg-background-secondary text-xs uppercase text-text-tertiary md:table-header-group">
          <tr>
            <th className="px-3 py-2 font-medium">{t('secrets.name', 'Name')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.action', 'Action')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.agent', 'Agent')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.access', 'Access')}</th>
            <th className="px-3 py-2 font-medium">{t('secrets.time', 'Time')}</th>
          </tr>
        </thead>
        <tbody className="block md:table-row-group">
          {records.map((r, i) => (
            <tr
              key={`${r.timestamp}-${i}`}
              className="block border-b border-border p-3 last:border-0 md:table-row md:p-0"
            >
              <AuditCell label={t('secrets.name', 'Name')} className="font-mono text-text-primary">
                {r.secret_name}
              </AuditCell>
              <AuditCell label={t('secrets.action', 'Action')} className="text-text-secondary">
                {r.action}
              </AuditCell>
              <AuditCell label={t('secrets.agent', 'Agent')} className="text-text-secondary">
                {r.agent_id || '—'}
              </AuditCell>
              <AuditCell label={t('secrets.access', 'Access')}>
                {r.granted ? (
                  <span className="text-state-success">{t('secrets.granted', 'Granted')}</span>
                ) : (
                  <span className="text-state-error">{t('secrets.denied', 'Denied')}</span>
                )}
              </AuditCell>
              <AuditCell label={t('secrets.time', 'Time')} className="text-xs text-text-tertiary">
                {formatTimestamp(r.timestamp)}
              </AuditCell>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function SecretsPage() {
  const { t } = useTranslation()
  const { sidebarOpen, mobileSidebarOpen, onCloseMobileSidebar, onOpenMobileSidebar, agents } =
    useAppLogicContext()
  const {
    secrets,
    status,
    audit,
    loading,
    error,
    refresh,
    refreshAudit,
    reveal,
    createSecret,
    updateSecret,
    removeSecret,
  } = useSecrets()
  const [form, setForm] = useState<
    { mode: 'create' } | { mode: 'edit'; secret: SecretMeta } | null
  >(null)
  const [formBusy, setFormBusy] = useState(false)
  const [busyName, setBusyName] = useState<string | null>(null)
  const [tab, setTab] = useState<'secrets' | 'audit'>('secrets')

  const openCreate = useCallback(() => setForm({ mode: 'create' }), [])
  const openEdit = useCallback((secret: SecretMeta) => setForm({ mode: 'edit', secret }), [])
  const closeForm = useCallback(() => setForm(null), [])

  const handleFormSubmit = useCallback(
    async (input: SecretInput) => {
      setFormBusy(true)
      try {
        if (form?.mode === 'edit') {
          await updateSecret(form.secret.name, {
            value: input.value,
            description: input.description,
            tags: input.tags,
            scope: input.scope,
          })
        } else {
          await createSecret(input)
        }
      } finally {
        setFormBusy(false)
      }
    },
    [form, createSecret, updateSecret],
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
    `whitespace-nowrap rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
      active
        ? 'bg-focus text-text-on-accent'
        : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
    }`

  return (
    <div className="flex app-shell bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={mobileSidebarOpen}
        onClose={() => onCloseMobileSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <header className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-2 border-b border-border bg-background-secondary px-4 py-3 md:flex-nowrap md:gap-x-3 md:px-6 md:py-4">
          <div className="flex min-w-0 flex-1 items-center gap-2 md:gap-3">
            <button
              type="button"
              onClick={() => onOpenMobileSidebar()}
              className="flex shrink-0 rounded-lg p-1.5 text-text-tertiary transition-colors hover:bg-background-tertiary hover:text-text-primary md:hidden"
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
            <BackButton />
            <h1 className="min-w-0 truncate text-base font-semibold text-text-primary">
              {t('secrets.title', 'Secrets')}
            </h1>
            {status && (
              <span className="hidden items-center gap-2 text-xs text-text-tertiary lg:flex">
                <BackendBadge backend={status.backend} />
                <span className="whitespace-nowrap">
                  {status.count} {t('secrets.count', 'secrets')}
                </span>
              </span>
            )}
          </div>

          <div className="flex shrink-0 items-center gap-1.5 md:order-2 md:gap-2">
            {tab === 'secrets' && (
              <Button type="button" variant="primary" size="md" onClick={openCreate}>
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
              </Button>
            )}
            <button
              type="button"
              onClick={handleRefresh}
              disabled={loading}
              aria-label={t('common.refresh', 'Refresh')}
              title={t('common.refresh', 'Refresh')}
              className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-background-secondary p-2 text-sm font-medium text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-40 lg:px-3 lg:py-2"
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
              <span className="hidden lg:inline">{t('common.refresh', 'Refresh')}</span>
            </button>
          </div>

          <div className="order-3 flex w-full min-w-0 items-center gap-2 md:order-1 md:ml-auto md:w-auto">
            <div className="flex min-w-0 flex-1 items-center gap-1 rounded-lg border border-border p-0.5 md:flex-none">
              <button
                type="button"
                className={`${tabCls(tab === 'secrets')} flex-1 md:flex-none`}
                onClick={() => setTab('secrets')}
              >
                {t('secrets.title', 'Secrets')}
              </button>
              <button
                type="button"
                className={`${tabCls(tab === 'audit')} flex-1 md:flex-none`}
                onClick={() => setTab('audit')}
              >
                {t('secrets.audit', 'Audit Log')}
              </button>
            </div>
            {status && (
              <span className="whitespace-nowrap text-xs text-text-tertiary lg:hidden">
                {status.count} {t('secrets.count', 'secrets')}
              </span>
            )}
          </div>
        </header>
        <div className="flex-1 overflow-y-auto p-4 md:p-6">
          {error && <ErrorBanner message={error} />}

          {tab === 'audit' ? (
            <AuditLog records={audit} />
          ) : (
            <>
              {loading && secrets.length === 0 && (
                <div className="flex items-center justify-center py-20">
                  <div className="h-8 w-8 animate-spin rounded-full border-2 border-focus border-t-transparent" />
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
                      onEdit={openEdit}
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

      {form && (
        <SecretFormModal
          busy={formBusy}
          agents={agents}
          initial={form.mode === 'edit' ? form.secret : null}
          onClose={closeForm}
          onSubmit={handleFormSubmit}
        />
      )}
    </div>
  )
}
