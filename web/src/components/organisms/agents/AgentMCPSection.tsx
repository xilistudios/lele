/**
 * AgentMCPSection — "MCP" tab panel of /agents/:agentId.
 *
 * Read-mostly view of the agent's mcp.json stack: the merged inventory from
 * `GET /api/v1/mcp?agent_id=…` renders one row per server NAME, above the
 * per-layer file status it was resolved from. The per-row markup lives in
 * AgentMCPServerRow and the layer block in AgentMCPLayers (both extracted,
 * like the raw editor) — this file keeps fetching, filtering, the toggle
 * mutations and the editor wiring.
 *
 * Two rules shape every line of this panel:
 *
 *  - NO SECRET VALUES, ever. The API only sends key NAMES (`env_keys`,
 *    `header_keys`) and raw unexpanded strings (`command`/`url` keep `${VAR}`
 *    literal — render them as plain text, never interpolate). This component
 *    must never render, fetch or derive a value; there is deliberately no
 *    control that reads one.
 *  - WRITES GO THROUGH THE ROW'S LAYER. Each toggle PUTs to the layer that
 *    owns the winning copy (`row.layer`, never `auto`), so the write lands
 *    where the user is looking; a 409 `mcp_entry_shadowed` surfaces the
 *    server's message plus an explicit "write anyway (force)" retry.
 *
 * Like the Skills tab's workspace side, toggles write immediately (no Save);
 * unlike anything else on this page, the tab writes NO config at all
 * (`SECTION_PATHS.mcp = []`), so there is no dirty dot and no draft dependency
 * — only the id. Hence the prop shape: AgentConfigPage passes `agentId` alone.
 *
 * (react-query + AuthContext providers live in main.tsx.)
 */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  type MCPDriveToggleVars,
  mcpErrorCode,
  useAgentMCPInventory,
  useMCPDriveToggle,
  useMCPRawFile,
} from '../../../hooks/useAgentMCP'
import type { MCPLayerName, MCPQueryLayer, MCPServerRow } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'
import { CloseIcon } from '../../atoms/Icons'
import { AgentMCPLayers, isStoredLayer } from './AgentMCPLayers'
import { AgentMCPRawEditor, type MCPRawNav } from './AgentMCPRawEditor'
import { AgentMCPServerRow } from './AgentMCPServerRow'

type Props = {
  /** Agent id passed by AgentConfigPage — every MCP request is keyed with it. */
  agentId: string
}

/** Source filter over `row.layer`, client-side; `all` = every layer. */
type SourceFilter = 'all' | 'global' | 'agent' | 'project'

const FILTERS: readonly SourceFilter[] = ['all', 'global', 'agent', 'project']

export function AgentMCPSection({ agentId }: Props) {
  const { t } = useTranslation()
  const { data, isLoading, isError, refetch } = useAgentMCPInventory(agentId)
  const toggle = useMCPDriveToggle(agentId)
  const [filter, setFilter] = useState<SourceFilter>('all')
  const [dismissed, setDismissed] = useState<ReadonlySet<number>>(new Set())
  /**
   * Variables of the last toggle attempt. The mutation's error belongs to
   * that row (and gives the force retry something to replay), tracked here
   * instead of reading `toggle.variables` so the pairing survives refetches.
   */
  const [attempt, setAttempt] = useState<MCPDriveToggleVars | null>(null)

  // ---------- raw mcp.json editor (one open layer at a time) ----------
  /** Layer whose raw file is being edited; null ⇔ editor closed. */
  const [editing, setEditing] = useState<MCPLayerName | null>(null)
  /**
   * Dirty tracking PER LAYER: the draft the user typed vs. the baseline it
   * was loaded from (or last saved to). Both stay in the SECTION so a
   * remount of the editor (switching layers) never silently loses them —
   * losing them is exactly what the unsaved-changes warning asks to confirm.
   */
  const [edits, setEdits] = useState<
    Partial<Record<MCPLayerName, { draft: string; baseline: string }>>
  >({})
  /** Navigation held back while the open draft is dirty (inline warning). */
  const [nav, setNav] = useState<MCPRawNav | null>(null)

  // Lazy fetch: null layer keeps the query disabled while closed (and for
  // every layer the user is NOT editing).
  const raw = useMCPRawFile(agentId, editing)
  const rawFile = raw.data
  const openEdit = editing ? edits[editing] : undefined
  const isDirty = openEdit !== undefined && openEdit.draft !== openEdit.baseline

  // Seed/refresh the draft from the file: a NEW entry is created on load,
  // and a CLEAN one follows a refetch (save elsewhere, invalidation) — an
  // edited one is never clobbered; only the user's keystrokes own it.
  useEffect(() => {
    if (!editing || !rawFile) return
    setEdits((previous) => {
      const current = previous[editing]
      if (current && current.draft !== current.baseline) return previous
      if (current && current.baseline === rawFile.content) return previous
      return { ...previous, [editing]: { draft: rawFile.content, baseline: rawFile.content } }
    })
  }, [editing, rawFile])

  /** Open (or switch to) the editor of a stored layer. */
  const openEditor = (layer: MCPLayerName) => {
    if (layer === editing) return
    if (isDirty && editing) {
      setNav({ kind: 'switch', target: layer })
      return
    }
    setNav(null)
    setEditing(layer)
  }

  /** Forget one layer's draft (close/discard). */
  const dropEdit = (layer: MCPLayerName) => {
    setEdits((previous) => {
      if (!(layer in previous)) return previous
      const next = { ...previous }
      delete next[layer]
      return next
    })
  }

  /** Close the editor; a dirty draft must confirm its discard first. */
  const closeEditor = () => {
    if (isDirty && editing) {
      setNav({ kind: 'close' })
      return
    }
    if (editing) dropEdit(editing)
    setNav(null)
    setEditing(null)
  }

  /** The user confirmed the discard: proceed with the held-back navigation. */
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

  /** Save succeeded: `content` is the bytes on disk → new clean baseline. */
  const markSaved = (content: string) => {
    if (!editing) return
    setEdits((previous) => ({ ...previous, [editing]: { draft: content, baseline: content } }))
  }

  const layers = data?.layers ?? []
  const servers = data?.servers ?? []
  const warnings = data?.warnings ?? []
  const visibleServers = filter === 'all' ? servers : servers.filter((row) => row.layer === filter)

  const layerLabel = (layer: string) => (isStoredLayer(layer) ? t(`mcp.layer.${layer}`) : layer)

  /** Enable/disable one server in the layer that owns its winning copy. */
  const runToggle = (row: MCPServerRow) => {
    // `enabled` is a SET, not a FLIP — and for an `invalid` entry there is no
    // safe set: planEnable on an entry with no `disabled` key yields nil edits
    // (200 `changed:false`, a silent no-op) while planDisable would write
    // `disabled:true` and hide the verdict. The row component's invalid
    // branch never offers this call; the guard makes the no-op impossible
    // even if a stale handler fires.
    if (row.effective === 'invalid') return
    const vars: MCPDriveToggleVars = {
      // The ROW's concrete layer — never 'auto': the write must land in the
      // copy the user is looking at. `row.layer` is a plain string on the
      // wire (the server owns the set of layers); cast here, at the call
      // site, to the toggle route's union.
      layer: row.layer as MCPQueryLayer,
      name: row.name,
      // Only an `enabled` winner gets disabled; a disabled one is switched
      // on. `invalid` never reaches here (guard above) and `auto` never
      // appears (row.layer is resolved).
      enabled: row.effective !== 'enabled',
    }
    setAttempt(vars)
    toggle.mutate(vars)
  }

  /** Replay the failed attempt with `force=true` (409 `mcp_entry_shadowed`). */
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

  /**
   * The last successful toggle answered `changed:false`: the server wrote
   * NOTHING (its plan produced no edits). `toggle.data` is the full
   * MCPToggleResponse — the hook discards nothing — and `attempt` names the
   * row it belongs to; the pending/error guards keep a stale pairing from
   * showing while another attempt is in flight or failed.
   */
  const noChangeName =
    toggle.data && attempt && !toggle.isPending && !toggle.isError && !toggle.data.changed
      ? attempt.name
      : null

  // ---------- Loading (same a11y pattern as the sibling sections) ----------
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

  // ---------- Inventory failed to load ----------
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
      {/* NOTE: the empty state below is a ROW of this panel, never a
          replacement for it. `servers.length === 0` is the state this raw
          editor exists for (`{}`, an absent or broken mcp.json), and a broken
          file reaches the UI as `warnings[]` with zero servers — so the
          warnings block and the layers block (each with its Edit affordance)
          must render regardless, and only the SERVERS table is swapped for
          the empty card. */}
      {/* ---------- Inventory warnings (dismissible) ---------- */}
      {warnings.length > 0 && dismissed.size < warnings.length && (
        <div
          data-testid="mcp-warnings"
          className="mb-4 rounded-lg border border-state-warning/40 bg-state-warning/10 px-3 py-2 text-xs"
        >
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
      )}

      {/* ---------- Source filter strip (client-side, by row.layer). Only
          meaningful when there is a table to filter: with zero servers it
          stays hidden (same as the fully-empty inventory). ---------- */}
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

      {/* ---------- Layer status: what file backs each layer (own component;
          must keep rendering even when servers is empty — see there) ---------- */}
      <AgentMCPLayers layers={layers} layerLabel={layerLabel} onEdit={openEditor} />

      {/* ---------- per-layer raw mcp.json editor (lazy: mounted when open) ---------- */}
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

      {/* ---------- One row per server NAME. With zero servers the table is
          swapped for the empty-state row (the layers/warnings/editor above
          already rendered — the diagnostics never hide behind it). ---------- */}
      {servers.length === 0 ? (
        <div
          data-testid="mcp-empty"
          className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center"
        >
          <p className="text-sm text-text-secondary">{t('mcp.empty.title')}</p>
          <p className="text-xs text-text-tertiary">{t('mcp.empty.desc')}</p>
        </div>
      ) : visibleServers.length === 0 ? (
        <p data-testid="mcp-no-matches" className="py-6 text-center text-xs text-text-tertiary">
          {t('mcp.noMatches')}
        </p>
      ) : (
        <ul data-testid="mcp-rows" className="space-y-2">
          {visibleServers.map((row) => (
            <AgentMCPServerRow
              key={row.name}
              row={row}
              layers={layers}
              layerLabel={layerLabel}
              onEdit={openEditor}
              pending={toggle.isPending && attempt?.name === row.name}
              busy={toggle.isPending}
              onToggle={runToggle}
              error={attempt?.name === row.name && toggle.isError ? toggleError : null}
              forceOffered={forceOffered}
              onForceRetry={forceRetry}
              noChange={noChangeName === row.name}
            />
          ))}
        </ul>
      )}
    </div>
  )
}
