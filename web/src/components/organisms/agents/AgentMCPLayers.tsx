/**
 * AgentMCPLayers — the per-layer file status block of the MCP tab.
 *
 * Extracted from AgentMCPSection (which owns inventory fetching and the raw
 * editor wiring) so the panel file stays readable: this component renders one
 * row per layer of the mcp.json stack — its label, path (or "disabled"),
 * exists/missing state, aliasing, and the Edit affordance that opens that
 * layer's raw editor.
 *
 * Invariant this block must keep: it renders whenever the INVENTORY has
 * layers, regardless of `servers.length`. A broken or empty mcp.json yields
 * zero servers plus `warnings[]` — the exact state the raw editor exists to
 * repair — so this block (like the warnings block) must never be hidden
 * behind the empty-state card; only the servers TABLE is swapped for it.
 */
import { useTranslation } from 'react-i18next'
import type { MCPLayer, MCPLayerName } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'

/** Stored layers the UI can translate; anything else renders raw. */
export const STORED_LAYERS = ['global', 'agent', 'project'] as const

export function isStoredLayer(value: string): value is (typeof STORED_LAYERS)[number] {
  return (STORED_LAYERS as readonly string[]).includes(value)
}

type Props = {
  /** Inventory layers (low → high, aliases folded). */
  layers: MCPLayer[]
  /** Human label for a layer name — section-owned i18n helper. */
  layerLabel: (layer: string) => string
  /** Open the raw mcp.json editor of that layer (stored layers only). */
  onEdit: (layer: MCPLayerName) => void
}

export function AgentMCPLayers({ layers, layerLabel, onEdit }: Props) {
  const { t } = useTranslation()
  if (layers.length === 0) return null

  return (
    <div
      data-testid="mcp-layers"
      className="mb-4 max-w-[820px] rounded-lg border border-border bg-background-secondary p-3.5"
    >
      <p className="mb-3 text-xs font-medium text-text-tertiary">{t('mcp.layers.title')}</p>
      <ul className="space-y-2">
        {layers.map((layer) => (
          <li
            key={layer.layer}
            data-testid={`mcp-layer-${layer.layer}`}
            className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs"
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
              <span data-testid={`mcp-layer-aliased-${layer.layer}`} className="text-text-tertiary">
                {t('mcp.layers.aliasedWith', {
                  layers: layer.aliased_with.map(layerLabel).join(', '),
                })}
              </span>
            )}

            {/* Raw editor affordance: one per layer row. A layer without
                path is disabled (never read/written); an unknown layer the
                raw routes would refuse (invalid_layer) is disabled too. */}
            <span className="ml-auto flex-none pl-2">
              <Button
                variant="secondary"
                size="sm"
                data-testid={`mcp-edit-${layer.layer}`}
                disabled={!layer.path || !isStoredLayer(layer.layer)}
                title={!layer.path ? t('mcp.editor.noPath') : undefined}
                aria-label={t('mcp.editor.editAria', { layer: layerLabel(layer.layer) })}
                onClick={() => onEdit(layer.layer as MCPLayerName)}
              >
                {t('mcp.editor.edit')}
              </Button>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}
