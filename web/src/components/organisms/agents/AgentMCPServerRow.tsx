import { useTranslation } from 'react-i18next'
import type { MCPEffective, MCPLayer, MCPLayerName, MCPServerRow } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'
import { isStoredLayer } from './AgentMCPLayers'

const STATE_GLYPH: Record<MCPEffective, string> = { enabled: '●', disabled: '○', invalid: '⚠' }
const STATE_CLASS: Record<MCPEffective, string> = {
  enabled: 'text-state-success',
  disabled: 'text-text-tertiary',
  invalid: 'text-state-warning',
}

const CHIP_CLS =
  'inline-flex items-center rounded-md border border-border bg-background-secondary px-1.5 py-0.5 font-mono text-2xs text-text-secondary'

const AUTH_CHIP_CLS: Record<'authenticated' | 'required', string> = {
  authenticated: `${CHIP_CLS} text-state-success`,
  required: `${CHIP_CLS} text-state-warning`,
}

type Props = {
  row: MCPServerRow
  layers: MCPLayer[]
  layerLabel: (layer: string) => string
  onEdit: (layer: MCPLayerName) => void
  pending: boolean
  busy: boolean
  onToggle: (row: MCPServerRow) => void
  error: string | null
  errorCode?: string | null
  forceOffered: boolean
  onForceRetry: () => void
  noChange: boolean
  authBusy: boolean
  onAuthenticate: (row: MCPServerRow) => void
  onLogout: (row: MCPServerRow) => void
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
  errorCode,
  forceOffered,
  onForceRetry,
  noChange,
  authBusy,
  onAuthenticate,
  onLogout,
}: Props) {
  const { t } = useTranslation()
  const rowLayer = layers.find((layer) => layer.layer === row.layer)
  const repairBlocked = !isStoredLayer(row.layer) || !rowLayer?.path

  const auth = row.server.auth
  const authStatus: 'authenticated' | 'required' | null = auth?.authenticated
    ? 'authenticated'
    : auth?.required
      ? 'required'
      : null
  const authenticated = authStatus === 'authenticated'
  const authRequired = authStatus === 'required'

  return (
    <li
      data-testid={`mcp-row-${row.name}`}
      className="flex gap-2.5 rounded-lg border border-border bg-background-primary p-3.5"
    >
      <span
        data-testid={`mcp-state-${row.name}`}
        aria-hidden="true"
        className={`mt-0.5 flex h-5 w-5 flex-none items-center justify-center text-sm leading-none ${STATE_CLASS[row.effective]}`}
      >
        {STATE_GLYPH[row.effective]}
      </span>
      <span className="sr-only">{t(`mcp.state.${row.effective}`)}</span>

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span
            data-testid={`mcp-name-${row.name}`}
            className="font-mono text-sm font-medium text-text-primary"
          >
            {row.name}
          </span>

          <span data-testid={`mcp-layer-tag-${row.name}`} className={CHIP_CLS}>
            {layerLabel(row.layer)}
          </span>

          {authStatus !== null && (
            <span data-testid={`mcp-auth-chip-${row.name}`} className={AUTH_CHIP_CLS[authStatus]}>
              {t(authenticated ? 'mcp.auth.statusAuthenticated' : 'mcp.auth.statusRequired')}
            </span>
          )}

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

            {authRequired && (
              <Button
                variant="secondary"
                size="sm"
                data-testid={`mcp-auth-start-${row.name}`}
                disabled={authBusy}
                aria-label={t('mcp.auth.buttonAria', { name: row.name })}
                onClick={() => onAuthenticate(row)}
              >
                {t('mcp.auth.button')}
              </Button>
            )}
            {authenticated && (
              <Button
                variant="secondary"
                size="sm"
                data-testid={`mcp-auth-logout-${row.name}`}
                disabled={authBusy}
                aria-label={t('mcp.auth.logoutAria', { name: row.name })}
                onClick={() => onLogout(row)}
              >
                {t('mcp.auth.logout')}
              </Button>
            )}
          </span>
        </div>

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

        {row.effective === 'invalid' && (
          <p
            data-testid={`mcp-repair-hint-${row.name}`}
            className="mt-2 text-xs text-text-secondary"
          >
            {t('mcp.toggle.invalidHint')}
          </p>
        )}

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

        {error !== null && (
          <div
            role="alert"
            data-testid={`mcp-row-error-${row.name}`}
            className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
          >
            <span className="min-w-0 flex-1">{error}</span>
            {errorCode !== null && errorCode !== undefined && (
              <span data-testid={`mcp-row-error-${row.name}-code`} className="font-mono text-2xs">
                {errorCode}
              </span>
            )}
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
      </div>
    </li>
  )
}
