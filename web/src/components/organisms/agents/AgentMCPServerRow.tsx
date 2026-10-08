/**
 * AgentMCPServerRow — one server NAME row of the MCP tab's merged inventory.
 *
 * Extracted from AgentMCPSection (which owns fetching, filtering, the toggle
 * mutations and the raw editor wiring) so the panel file stays readable: this
 * component renders the state glyph + sr-only verdict (§10.5), the winning
 * layer tag, the RAW command/url (`${VAR}` literal, never interpolated), the
 * key NAME chips, the invalid/shadow diagnostics and the row's write
 * controls.
 *
 * Two rules it must keep:
 *
 *  - NO SECRET VALUES. Everything here is a key NAME or a raw unexpanded
 *    string; no control may fetch or render a value (there is no such
 *    endpoint, ever).
 *  - AN INVALID ENTRY HAS NO WORKING TOGGLE. `enabled` on the toggle route is
 *    a SET, not a FLIP: planEnable on an entry with no `disabled` key answers
 *    200 `changed:false` (a silent no-op) and a `disabled:true` write would
 *    hide the verdict. So the toggle renders disabled with the reason, the
 *    row offers the SAME Edit affordance as the layers block (one click from
 *    the file that repairs it), and a `changed:false` write is reported
 *    inline (message + machine code chip, house idiom) instead of pretending
 *    the row moved.
 */
import { useTranslation } from 'react-i18next'
import type { MCPEffective, MCPLayer, MCPLayerName, MCPServerRow } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'
import { isStoredLayer } from './AgentMCPLayers'

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

type Props = {
  row: MCPServerRow
  /** Inventory layers — repair mirrors mcp-edit-<layer> (needs the path). */
  layers: MCPLayer[]
  /** Human label for a layer name — section-owned i18n helper. */
  layerLabel: (layer: string) => string
  /** Open the raw editor of a layer (SAME handler as the layers block). */
  onEdit: (layer: MCPLayerName) => void
  /** A toggle write is in flight FOR THIS ROW (disables its toggle). */
  pending: boolean
  /** ANY toggle write is in flight (disables the force replay). */
  busy: boolean
  onToggle: (row: MCPServerRow) => void
  /** Failed-write message for this row (already paired), or null. */
  error: string | null
  /** The failure is 409 `mcp_entry_shadowed` → offer the force retry. */
  forceOffered: boolean
  onForceRetry: () => void
  /** The last successful toggle for this row answered `changed:false`. */
  noChange: boolean
}

export function AgentMCPServerRow({
  row,
  layers,
  layerLabel,
  onEdit,
  pending,
  busy,
  onToggle,
  error,
  forceOffered,
  onForceRetry,
  noChange,
}: Props) {
  const { t } = useTranslation()
  // Repair wiring mirrors the layers block: no path (or an unknown layer)
  // ⇒ nothing to edit, exactly like mcp-edit-<layer>.
  const rowLayer = layers.find((layer) => layer.layer === row.layer)
  const repairBlocked = !isStoredLayer(row.layer) || !rowLayer?.path

  return (
    <li
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

        <span className="ml-auto flex-none flex items-center gap-2">
          {row.effective === 'invalid' ? (
            // INVALID: no working toggle. `enabled` is a SET, not a FLIP —
            // planEnable here answers 200 `changed:false` (a silent no-op)
            // and a `disabled:true` write would hide the verdict. The row is
            // wired to the SAME Edit affordance as the layers block
            // (mcp-edit-<layer> uses this same onEdit): one click from the
            // file that repairs it.
            <>
              <Button
                variant="secondary"
                size="sm"
                data-testid={`mcp-toggle-${row.name}`}
                disabled
                title={t('mcp.toggle.invalidHint')}
              >
                {t('mcp.toggle.invalidLabel')}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                data-testid={`mcp-repair-${row.name}`}
                disabled={repairBlocked}
                title={repairBlocked ? t('mcp.editor.noPath') : undefined}
                aria-label={t('mcp.editor.editAria', { layer: layerLabel(row.layer) })}
                onClick={() => onEdit(row.layer as MCPLayerName)}
              >
                {t('mcp.editor.edit')}
              </Button>
            </>
          ) : (
            <Button
              variant="secondary"
              size="sm"
              data-testid={`mcp-toggle-${row.name}`}
              disabled={pending}
              aria-label={
                row.effective === 'enabled'
                  ? t('mcp.toggle.disableAria', { name: row.name })
                  : t('mcp.toggle.enableAria', { name: row.name })
              }
              onClick={() => onToggle(row)}
            >
              {row.effective === 'enabled' ? t('mcp.toggle.disable') : t('mcp.toggle.enable')}
            </Button>
          )}
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
            <span key={key} data-testid={`mcp-envkey-${row.name}-${key}`} className={CHIP_CLS}>
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
            <span key={key} data-testid={`mcp-headerkey-${row.name}-${key}`} className={CHIP_CLS}>
              {key}
            </span>
          ))}
        </div>
      )}

      {row.server.invalid && (
        <p data-testid={`mcp-invalid-${row.name}`} className="mt-2 text-xs text-state-warning">
          {row.server.invalid}
        </p>
      )}

      {/* Why there is no toggle here + where to fix it: visible text, not
          just the button tooltip (§10.5). */}
      {row.effective === 'invalid' && (
        <p data-testid={`mcp-repair-hint-${row.name}`} className="mt-2 text-xs text-text-secondary">
          {t('mcp.toggle.invalidHint')}
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
      {error !== null && (
        <div
          role="alert"
          data-testid={`mcp-row-error-${row.name}`}
          className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
        >
          <span className="min-w-0 flex-1">{error}</span>
          {forceOffered && (
            <Button
              variant="secondary"
              size="sm"
              data-testid={`mcp-force-${row.name}`}
              disabled={busy}
              onClick={onForceRetry}
            >
              {t('mcp.force.label')}
            </Button>
          )}
        </div>
      )}

      {/* A 200 that wrote NOTHING (`changed:false`): honest inline report
          (message + machine code chip, house idiom — never a toast) instead
          of letting the inventory refetch look like the row moved. */}
      {noChange && (
        <output
          data-testid={`mcp-row-nochange-${row.name}`}
          className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-background-secondary px-3 py-2 text-xs text-text-secondary"
        >
          <span className="min-w-0 flex-1">{t('mcp.toggle.noChange')}</span>
          <span
            data-testid={`mcp-row-nochange-${row.name}-code`}
            className="font-mono text-2xs text-text-tertiary"
          >
            changed:false
          </span>
        </output>
      )}
    </li>
  )
}
