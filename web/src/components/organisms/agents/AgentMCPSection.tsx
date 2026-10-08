/**
 * AgentMCPSection — "MCP" tab panel of /agents/:agentId.
 *
 * Read-mostly view of the agent's mcp.json stack: the merged inventory from
 * `GET /api/v1/mcp?agent_id=…` renders one row per server NAME, above the
 * per-layer file status it was resolved from.
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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  type MCPDriveToggleVars,
  mcpErrorCode,
  useAgentMCPInventory,
  useMCPDriveToggle,
} from '../../../hooks/useAgentMCP'
import type { MCPEffective, MCPQueryLayer, MCPServerRow } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'
import { CloseIcon } from '../../atoms/Icons'

type Props = {
  /** Agent id passed by AgentConfigPage — every MCP request is keyed with it. */
  agentId: string
}

/** Source filter over `row.layer`, client-side; `all` = every layer. */
type SourceFilter = 'all' | 'global' | 'agent' | 'project'

const FILTERS: readonly SourceFilter[] = ['all', 'global', 'agent', 'project']

/** Stored layers the UI can translate; anything else renders raw. */
const STORED_LAYERS = ['global', 'agent', 'project'] as const

function isStoredLayer(value: string): value is (typeof STORED_LAYERS)[number] {
  return (STORED_LAYERS as readonly string[]).includes(value)
}

/** State indicator per `effective` (§10.5: never colour-only, sr-only text rides along). */
const STATE_GLYPH: Record<MCPEffective, string> = { enabled: '●', disabled: '○', invalid: '⚠' }
const STATE_CLASS: Record<MCPEffective, string> = {
  enabled: 'text-state-success',
  disabled: 'text-text-tertiary',
  invalid: 'text-state-warning',
}

/** One chip style for layer tags, key NAMES and shadow copies. */
const CHIP_CLS =
  'inline-flex items-center rounded-md border border-border bg-background-secondary px-1.5 py-0.5 font-mono text-2xs text-text-secondary'

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

  const layers = data?.layers ?? []
  const servers = data?.servers ?? []
  const warnings = data?.warnings ?? []
  const visibleServers = filter === 'all' ? servers : servers.filter((row) => row.layer === filter)

  const layerLabel = (layer: string) => (isStoredLayer(layer) ? t(`mcp.layer.${layer}`) : layer)

  /** Enable/disable one server in the layer that owns its winning copy. */
  const runToggle = (row: MCPServerRow) => {
    const vars: MCPDriveToggleVars = {
      // The ROW's concrete layer — never 'auto': the write must land in the
      // copy the user is looking at. `row.layer` is a plain string on the
      // wire (the server owns the set of layers); cast here, at the call
      // site, to the toggle route's union.
      layer: row.layer as MCPQueryLayer,
      name: row.name,
      // Only an `enabled` winner gets disabled; anything else (disabled or
      // invalid-but-toggleable) is switched on.
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

  // ---------- Empty: no layer defines any server ----------
  if (servers.length === 0) {
    return (
      <div
        data-testid="mcp-empty"
        className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center"
      >
        <p className="text-sm text-text-secondary">{t('mcp.empty.title')}</p>
        <p className="text-xs text-text-tertiary">{t('mcp.empty.desc')}</p>
      </div>
    )
  }

  return (
    <div>
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

      {/* ---------- Source filter strip (client-side, by row.layer) ---------- */}
      {/* biome-ignore lint/a11y/useSemanticElements: a chip strip is not a form grouping — <fieldset> would drag in form semantics and UA borders */}
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

      {/* ---------- Layer status: what file backs each layer ---------- */}
      {layers.length > 0 && (
        <div
          data-testid="mcp-layers"
          className="mb-4 rounded-lg border border-border bg-background-secondary p-3.5"
        >
          <p className="mb-2 text-xs font-medium text-text-tertiary">{t('mcp.layers.title')}</p>
          <ul className="space-y-2">
            {layers.map((layer) => (
              <li
                key={layer.layer}
                data-testid={`mcp-layer-${layer.layer}`}
                className="flex flex-wrap items-center gap-2 text-xs"
              >
                <span className="font-medium text-text-primary">{layerLabel(layer.layer)}</span>
                {layer.path ? (
                  <code
                    data-testid={`mcp-layer-path-${layer.layer}`}
                    className="min-w-0 break-all font-mono text-text-secondary"
                  >
                    {layer.path}
                  </code>
                ) : (
                  // Absent path ⇔ disabled layer (no workspace root): never read/written.
                  <span
                    data-testid={`mcp-layer-disabled-${layer.layer}`}
                    className="text-state-warning"
                  >
                    {t('mcp.layers.disabled')}
                  </span>
                )}
                <span
                  data-testid={`mcp-layer-exists-${layer.layer}`}
                  className={layer.exists ? 'text-state-success' : 'text-text-tertiary'}
                >
                  {layer.exists ? t('mcp.layers.exists') : t('mcp.layers.missing')}
                </span>
                {layer.aliased_with && layer.aliased_with.length > 0 && (
                  <span
                    data-testid={`mcp-layer-aliased-${layer.layer}`}
                    className="text-text-tertiary"
                  >
                    {t('mcp.layers.aliasedWith', {
                      layers: layer.aliased_with.map(layerLabel).join(', '),
                    })}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* ---------- One row per server NAME ---------- */}
      {visibleServers.length === 0 ? (
        <p data-testid="mcp-no-matches" className="py-6 text-center text-xs text-text-tertiary">
          {t('mcp.noMatches')}
        </p>
      ) : (
        <ul data-testid="mcp-rows" className="space-y-2">
          {visibleServers.map((row) => (
            <li
              key={row.name}
              data-testid={`mcp-row-${row.name}`}
              className="rounded-lg border border-border bg-background-primary p-3.5"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span
                  data-testid={`mcp-state-${row.name}`}
                  aria-hidden="true"
                  className={`text-sm leading-none ${STATE_CLASS[row.effective]}`}
                >
                  {STATE_GLYPH[row.effective]}
                </span>
                {/* Never glyph-only (§10.5). */}
                <span className="sr-only">{t(`mcp.state.${row.effective}`)}</span>

                <span
                  data-testid={`mcp-name-${row.name}`}
                  className="font-mono text-sm font-medium text-text-primary"
                >
                  {row.name}
                </span>

                {/* Winning layer tag — also the layer every write uses. */}
                <span data-testid={`mcp-layer-tag-${row.name}`} className={CHIP_CLS}>
                  {layerLabel(row.layer)}
                </span>

                {/* RAW strings: `${VAR}` must show literally — no interpolation, ever. */}
                {row.server.command !== undefined && (
                  <code
                    data-testid={`mcp-command-${row.name}`}
                    className="min-w-0 break-all font-mono text-xs text-text-secondary"
                  >
                    {row.server.command}
                  </code>
                )}
                {row.server.url !== undefined && (
                  <code
                    data-testid={`mcp-url-${row.name}`}
                    className="min-w-0 break-all font-mono text-xs text-text-secondary"
                  >
                    {row.server.url}
                  </code>
                )}

                {row.server.args !== undefined && row.server.args > 0 && (
                  <span data-testid={`mcp-args-${row.name}`} className={CHIP_CLS}>
                    {t('mcp.row.args', { n: row.server.args })}
                  </span>
                )}

                <span className="ml-auto flex-none">
                  <Button
                    variant="secondary"
                    size="sm"
                    data-testid={`mcp-toggle-${row.name}`}
                    disabled={toggle.isPending && attempt?.name === row.name}
                    aria-label={
                      row.effective === 'enabled'
                        ? t('mcp.toggle.disableAria', { name: row.name })
                        : t('mcp.toggle.enableAria', { name: row.name })
                    }
                    onClick={() => runToggle(row)}
                  >
                    {row.effective === 'enabled' ? t('mcp.toggle.disable') : t('mcp.toggle.enable')}
                  </Button>
                </span>
              </div>

              {/* Key NAMES only — the API never sends values and none is ever fetched. */}
              {row.server.env_keys && row.server.env_keys.length > 0 && (
                <div
                  data-testid={`mcp-envkeys-${row.name}`}
                  className="mt-2 flex flex-wrap items-center gap-1.5"
                >
                  <span className="text-2xs uppercase tracking-wide text-text-tertiary">
                    {t('mcp.row.envKeys')}
                  </span>
                  {row.server.env_keys.map((key) => (
                    <span
                      key={key}
                      data-testid={`mcp-envkey-${row.name}-${key}`}
                      className={CHIP_CLS}
                    >
                      {key}
                    </span>
                  ))}
                </div>
              )}
              {row.server.header_keys && row.server.header_keys.length > 0 && (
                <div
                  data-testid={`mcp-headerkeys-${row.name}`}
                  className="mt-2 flex flex-wrap items-center gap-1.5"
                >
                  <span className="text-2xs uppercase tracking-wide text-text-tertiary">
                    {t('mcp.row.headerKeys')}
                  </span>
                  {row.server.header_keys.map((key) => (
                    <span
                      key={key}
                      data-testid={`mcp-headerkey-${row.name}-${key}`}
                      className={CHIP_CLS}
                    >
                      {key}
                    </span>
                  ))}
                </div>
              )}

              {row.server.invalid && (
                <p
                  data-testid={`mcp-invalid-${row.name}`}
                  className="mt-2 text-xs text-state-warning"
                >
                  {row.server.invalid}
                </p>
              )}

              {/* Inert copies below the winner, each with the server's reason. */}
              {row.shadowed && row.shadowed.length > 0 && (
                <div
                  data-testid={`mcp-shadowed-${row.name}`}
                  className="mt-2 flex flex-wrap items-center gap-1.5"
                >
                  <span className="text-2xs uppercase tracking-wide text-text-tertiary">
                    {t('mcp.row.shadowed')}
                  </span>
                  {row.shadowed.map((shadow) => (
                    <span
                      key={`${shadow.layer}:${shadow.path ?? ''}`}
                      data-testid={`mcp-shadow-${row.name}-${shadow.layer}`}
                      title={shadow.reason}
                      className={CHIP_CLS}
                    >
                      {layerLabel(shadow.layer)}: {shadow.reason}
                    </span>
                  ))}
                </div>
              )}

              {/* A failed write has nowhere else to surface (same as Skills). */}
              {attempt && attempt.name === row.name && toggle.isError && (
                <div
                  role="alert"
                  data-testid={`mcp-row-error-${row.name}`}
                  className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
                >
                  <span className="min-w-0 flex-1">{toggleError}</span>
                  {forceOffered && (
                    <Button
                      variant="secondary"
                      size="sm"
                      data-testid={`mcp-force-${row.name}`}
                      disabled={toggle.isPending}
                      onClick={forceRetry}
                    >
                      {t('mcp.force.label')}
                    </Button>
                  )}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
