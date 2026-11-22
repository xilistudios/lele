import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  type MCPDriveToggleVars,
  mcpErrorCode,
  useAgentMCPInventory,
  useMCPDriveToggle,
  useMCPLogout,
  useMCPRawFile,
} from '../../../hooks/useAgentMCP'
import type { MCPLayerName, MCPQueryLayer, MCPServerRow } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'
import { CloseIcon } from '../../atoms/Icons'
import { AgentMCPLayers, isStoredLayer } from './AgentMCPLayers'
import { AgentMCPRawEditor, type MCPRawNav } from './AgentMCPRawEditor'
import { AgentMCPServerRow } from './AgentMCPServerRow'
import { MCPAuthModal } from './MCPAuthModal'

type Props = {
  agentId: string
}

type SourceFilter = 'all' | 'global' | 'agent' | 'project'

const FILTERS: readonly SourceFilter[] = ['all', 'global', 'agent', 'project']

export function AgentMCPSection({ agentId }: Props) {
  const { t } = useTranslation()
  const { data, isLoading, isError, refetch } = useAgentMCPInventory(agentId)
  const toggle = useMCPDriveToggle(agentId)
  const [filter, setFilter] = useState<SourceFilter>('all')
  const [dismissed, setDismissed] = useState<ReadonlySet<number>>(new Set())
  const [attempt, setAttempt] = useState<MCPDriveToggleVars | null>(null)

  const [editing, setEditing] = useState<MCPLayerName | null>(null)
  const [edits, setEdits] = useState<
    Partial<Record<MCPLayerName, { draft: string; baseline: string }>>
  >({})
  const [nav, setNav] = useState<MCPRawNav | null>(null)

  const [authServer, setAuthServer] = useState<string | null>(null)
  const [logoutName, setLogoutName] = useState<string | null>(null)
  const logout = useMCPLogout(agentId)

  const raw = useMCPRawFile(agentId, editing)
  const rawFile = raw.data
  const openEdit = editing ? edits[editing] : undefined
  const isDirty = openEdit !== undefined && openEdit.draft !== openEdit.baseline

  useEffect(() => {
    if (!editing || !rawFile) return
    setEdits((previous) => {
      const current = previous[editing]
      if (current && current.draft !== current.baseline) return previous
      if (current && current.baseline === rawFile.content) return previous
      return { ...previous, [editing]: { draft: rawFile.content, baseline: rawFile.content } }
    })
  }, [editing, rawFile])

  const openEditor = (layer: MCPLayerName) => {
    if (layer === editing) return
    if (isDirty && editing) {
      setNav({ kind: 'switch', target: layer })
      return
    }
    setNav(null)
    setEditing(layer)
  }

  const dropEdit = (layer: MCPLayerName) => {
    setEdits((previous) => {
      if (!(layer in previous)) return previous
      const next = { ...previous }
      delete next[layer]
      return next
    })
  }

  const closeEditor = () => {
    if (isDirty && editing) {
      setNav({ kind: 'close' })
      return
    }
    if (editing) dropEdit(editing)
    setNav(null)
    setEditing(null)
  }

  const confirmNav = () => {
    if (!nav) return
    if (editing) dropEdit(editing)
    setNav(null)
    setEditing(nav.kind === 'close' ? null : nav.target)
  }

  const changeDraft = (value: string) => {
    if (!editing) return
    setEdits((previous) => ({
      ...previous,
      [editing]: { draft: value, baseline: previous[editing]?.baseline ?? rawFile?.content ?? '' },
    }))
  }

  const markSaved = (content: string) => {
    if (!editing) return
    setEdits((previous) => ({ ...previous, [editing]: { draft: content, baseline: content } }))
  }

  const layers = data?.layers ?? []
  const servers = data?.servers ?? []
  const warnings = data?.warnings ?? []
  const visibleServers = filter === 'all' ? servers : servers.filter((row) => row.layer === filter)

  const layerLabel = (layer: string) => (isStoredLayer(layer) ? t(`mcp.layer.${layer}`) : layer)

  const runToggle = (row: MCPServerRow) => {
    if (row.effective === 'invalid') return
    const vars: MCPDriveToggleVars = {
      layer: row.layer as MCPQueryLayer,
      name: row.name,
      enabled: row.effective !== 'enabled',
    }
    setAttempt(vars)
    toggle.mutate(vars)
  }

  const forceRetry = () => {
    if (!attempt) return
    const vars: MCPDriveToggleVars = { ...attempt, force: true }
    setAttempt(vars)
    toggle.mutate(vars)
  }

  const toggleError = toggle.isError
    ? toggle.error instanceof Error
      ? toggle.error.message
      : String(toggle.error)
    : ''
  const forceOffered = toggle.isError && mcpErrorCode(toggle.error) === 'mcp_entry_shadowed'

  const runLogout = (row: MCPServerRow) => {
    setLogoutName(row.name)
    logout.mutate(row.name)
  }

  const rowFailure = (
    name: string,
  ): { message: string; code: string | undefined; force: boolean } | null => {
    if (attempt?.name === name && toggle.isError) {
      return { message: toggleError, code: mcpErrorCode(toggle.error), force: forceOffered }
    }
    if (logoutName === name && logout.isError) {
      return {
        message: logout.error instanceof Error ? logout.error.message : String(logout.error),
        code: mcpErrorCode(logout.error),
        force: false,
      }
    }
    return null
  }

  const noChangeName =
    toggle.data && attempt && !toggle.isPending && !toggle.isError && !toggle.data.changed
      ? attempt.name
      : null

  if (isLoading) {
    return (
      <div aria-busy="true" data-testid="mcp-loading">
        <span className="sr-only">{t('mcp.loading')}</span>
        <div className="space-y-2">
          {[0, 1, 2].map((n) => (
            <div
              key={n}
              className="h-16 animate-pulse rounded-lg border border-border bg-background-secondary"
            />
          ))}
        </div>
      </div>
    )
  }

  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-surface-sunken px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">{t('mcp.loadError')}</p>
        <Button variant="secondary" size="sm" data-testid="mcp-retry" onClick={() => refetch()}>
          {t('mcp.retry')}
        </Button>
      </div>
    )
  }

  return (
    <div>
      {warnings.length > 0 && dismissed.size < warnings.length && (
        <div
          data-testid="mcp-warnings"
          className="mb-4 flex max-w-[820px] items-start gap-2.5 rounded-lg border border-state-warning/40 bg-state-warning/10 p-3.5 text-xs"
        >
          <div className="min-w-0 flex-1">
            <p className="mb-1 font-medium text-state-warning">{t('mcp.warnings.title')}</p>
            <ul className="space-y-1">
              {warnings.map((warning, index) =>
                dismissed.has(index) ? null : (
                  <li
                    key={`${index}:${warning}`}
                    data-testid={`mcp-warning-${index}`}
                    className="flex items-center gap-2"
                  >
                    <span className="min-w-0 flex-1 text-text-secondary">{warning}</span>
                    <button
                      type="button"
                      data-testid={`mcp-warning-dismiss-${index}`}
                      aria-label={t('mcp.warnings.dismiss')}
                      onClick={() => setDismissed((prev) => new Set(prev).add(index))}
                      className="flex-none text-text-tertiary transition-colors hover:text-text-primary"
                    >
                      <CloseIcon size={12} />
                    </button>
                  </li>
                ),
              )}
            </ul>
          </div>
        </div>
      )}

      {servers.length > 0 && (
        // biome-ignore lint/a11y/useSemanticElements: a chip strip is not a form grouping — <fieldset> would drag in form semantics and UA borders
        <div
          role="group"
          aria-label={t('mcp.filter.label')}
          data-testid="mcp-filter"
          className="mb-4 flex flex-wrap items-center gap-2"
        >
          {FILTERS.map((value) => (
            <button
              key={value}
              type="button"
              data-testid={`mcp-filter-${value}`}
              aria-pressed={filter === value}
              onClick={() => setFilter(value)}
              className={`rounded-md border px-2.5 py-1 text-xs font-medium transition-colors ${
                filter === value
                  ? 'border-accent-primary/30 bg-surface-selected text-accent-primary'
                  : 'border-border text-text-secondary hover:bg-surface-hover hover:text-text-primary'
              }`}
            >
              {value === 'all' ? t('mcp.filter.all') : t(`mcp.layer.${value}`)}
            </button>
          ))}
        </div>
      )}

      <AgentMCPLayers layers={layers} layerLabel={layerLabel} onEdit={openEditor} />

      {editing && (
        <AgentMCPRawEditor
          key={editing}
          agentId={agentId}
          layer={editing}
          file={rawFile}
          loading={raw.isLoading}
          loadFailed={raw.isError}
          onRetryLoad={() => void raw.refetch()}
          draft={openEdit?.draft ?? ''}
          layerLabel={layerLabel}
          nav={nav}
          onDraftChange={changeDraft}
          onSaved={markSaved}
          onClose={closeEditor}
          onConfirmNav={confirmNav}
          onDismissNav={() => setNav(null)}
        />
      )}

      {servers.length === 0 ? (
        <div
          data-testid="mcp-empty"
          className="max-w-[820px] flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center"
        >
          <p className="text-sm text-text-secondary">{t('mcp.empty.title')}</p>
          <p className="text-xs text-text-tertiary">{t('mcp.empty.desc')}</p>
        </div>
      ) : visibleServers.length === 0 ? (
        <p
          data-testid="mcp-no-matches"
          className="max-w-[820px] py-6 text-center text-xs text-text-tertiary"
        >
          {t('mcp.noMatches')}
        </p>
      ) : (
        <ul data-testid="mcp-rows" className="max-w-[820px] space-y-2.5">
          {visibleServers.map((row) => {
            const failure = rowFailure(row.name)
            return (
              <AgentMCPServerRow
                key={row.name}
                row={row}
                layers={layers}
                layerLabel={layerLabel}
                onEdit={openEditor}
                pending={toggle.isPending && attempt?.name === row.name}
                busy={toggle.isPending}
                onToggle={runToggle}
                error={failure?.message ?? null}
                errorCode={failure?.code ?? null}
                forceOffered={failure?.force ?? false}
                onForceRetry={forceRetry}
                noChange={noChangeName === row.name}
                authBusy={authServer === row.name || (logout.isPending && logoutName === row.name)}
                onAuthenticate={(target) => setAuthServer(target.name)}
                onLogout={runLogout}
              />
            )
          })}
        </ul>
      )}

      {authServer !== null && (
        <MCPAuthModal
          key={authServer}
          agentId={agentId}
          serverName={authServer}
          open
          onClose={() => setAuthServer(null)}
        />
      )}
    </div>
  )
}
