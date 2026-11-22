import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { mcpErrorCode, useMCPValidate, useSaveMCPRaw } from '../../../hooks/useAgentMCP'
import type { MCPLayerName, MCPRawFileResponse, MCPValidateResponse } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'

export type MCPRawNav = { kind: 'close' } | { kind: 'switch'; target: MCPLayerName }

type Props = {
  agentId: string
  layer: MCPLayerName
  file: MCPRawFileResponse | undefined
  loading: boolean
  loadFailed: boolean
  onRetryLoad: () => void
  draft: string
  layerLabel: (layer: string) => string
  nav: MCPRawNav | null
  onDraftChange: (content: string) => void
  onSaved: (content: string) => void
  onClose: () => void
  onConfirmNav: () => void
  onDismissNav: () => void
}

function ErrorBanner({
  testid,
  message,
  code,
}: { testid: string; message: string; code?: string }) {
  const { t } = useTranslation()
  return (
    <p
      role="alert"
      data-testid={testid}
      className="rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
    >
      <span className="block font-medium">{t('mcp.editor.errorLabel')}</span>
      <span className="mt-0.5 block font-mono text-xs break-all">{message}</span>
      {code && (
        <span data-testid={`${testid}-code`} className="mt-0.5 block font-mono text-2xs opacity-80">
          {code}
        </span>
      )}
    </p>
  )
}

function WarningList({ testid, title, items }: { testid: string; title: string; items: string[] }) {
  return (
    <div
      data-testid={testid}
      className="rounded-lg border border-state-warning/40 bg-state-warning/10 px-3 py-2 text-xs"
    >
      <p className="mb-1 font-medium text-state-warning">{title}</p>
      <ul className="space-y-1">
        {items.map((item, index) => (
          <li
            key={`${index}:${item}`}
            data-testid={`${testid}-${index}`}
            className="text-text-secondary"
          >
            {item}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function AgentMCPRawEditor({
  agentId,
  layer,
  file,
  loading,
  loadFailed,
  onRetryLoad,
  draft,
  layerLabel,
  nav,
  onDraftChange,
  onSaved,
  onClose,
  onConfirmNav,
  onDismissNav,
}: Props) {
  const { t } = useTranslation()
  const validate = useMCPValidate(agentId)
  const save = useSaveMCPRaw(agentId, layer)

  const [validation, setValidation] = useState<MCPValidateResponse | null>(null)
  const [validateError, setValidateError] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<{ message: string; code?: string } | null>(null)
  const [savedOk, setSavedOk] = useState(false)
  const [savedWarnings, setSavedWarnings] = useState<string[]>([])

  const handleValidate = () => {
    setValidateError(null)
    validate.mutate(
      { layer, content: draft },
      {
        onSuccess: (data) => setValidation(data),
        onError: (error) =>
          setValidateError(error instanceof Error ? error.message : String(error)),
      },
    )
  }

  const handleSave = () => {
    setSaveError(null)
    save.mutate(draft, {
      onSuccess: (data) => {
        setSavedOk(true)
        setSavedWarnings(data.warnings ?? [])
        onSaved(data.content)
      },
      onError: (error) => {
        setSavedOk(false)
        setSavedWarnings([])
        setSaveError({
          message: error instanceof Error ? error.message : String(error),
          code: mcpErrorCode(error),
        })
      },
    })
  }

  const handleChange = (value: string) => {
    setValidation(null)
    setValidateError(null)
    setSavedOk(false)
    setSavedWarnings([])
    onDraftChange(value)
  }

  const validateFatal = validation?.error ? validation.error : ''
  const validateWarnings = validation?.warnings ?? []
  const aliased = file?.aliased_with ?? []

  return (
    <div
      data-testid="mcp-editor"
      className="mb-4 rounded-lg border border-border bg-background-primary p-3.5"
    >
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <p data-testid="mcp-editor-title" className="text-xs font-medium text-text-primary">
          {t('mcp.editor.title', { layer: t(`mcp.layer.${layer}`) })}
        </p>
      </div>

      {nav && (
        <div
          role="alert"
          data-testid="mcp-editor-unsaved"
          className="mb-3 rounded-lg border border-state-warning/40 bg-state-warning/10 px-3 py-2 text-xs"
        >
          <p className="mb-1 font-medium text-state-warning">{t('mcp.editor.unsavedTitle')}</p>
          <p className="mb-2 text-text-secondary">
            {nav.kind === 'close' ? t('mcp.editor.unsavedClose') : t('mcp.editor.unsavedSwitch')}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="secondary"
              size="sm"
              data-testid="mcp-editor-unsaved-confirm"
              onClick={onConfirmNav}
            >
              {nav.kind === 'close' ? t('mcp.editor.discardClose') : t('mcp.editor.discardSwitch')}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              data-testid="mcp-editor-unsaved-cancel"
              onClick={onDismissNav}
            >
              {nav.kind === 'close' ? t('mcp.editor.keepEditing') : t('mcp.editor.stay')}
            </Button>
          </div>
        </div>
      )}

      {file && (
        <div
          data-testid="mcp-editor-path-block"
          className="mb-3 flex flex-wrap items-baseline gap-2 rounded-lg border border-border bg-background-secondary/60 px-3 py-2"
        >
          <span className="text-2xs uppercase tracking-wide text-text-tertiary">
            {t('mcp.editor.pathLabel')}
          </span>
          <code
            data-testid="mcp-editor-path"
            className="min-w-0 break-all font-mono text-xs text-text-secondary"
          >
            {file.path ?? ''}
          </code>
        </div>
      )}

      {file && !file.exists && (
        <p data-testid="mcp-editor-create-hint" className="mb-3 text-xs text-text-secondary">
          {t('mcp.editor.createHint')}
        </p>
      )}

      {aliased.length > 0 && (
        <div
          role="alert"
          data-testid="mcp-editor-aliased"
          className="mb-3 rounded-lg border border-state-warning/50 bg-state-warning/15 px-3 py-2 text-xs font-medium text-state-warning"
        >
          {t('mcp.editor.aliased', { layers: aliased.map(layerLabel).join(', ') })}
        </div>
      )}

      {loading && (
        <p data-testid="mcp-editor-loading" className="mb-3 text-xs text-text-tertiary">
          {t('mcp.editor.loading')}
        </p>
      )}
      {!loading && loadFailed && (
        <div
          role="alert"
          data-testid="mcp-editor-load-error"
          className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
        >
          <span className="min-w-0 flex-1">{t('mcp.editor.loadError')}</span>
          <Button
            variant="secondary"
            size="sm"
            data-testid="mcp-editor-retry"
            onClick={onRetryLoad}
          >
            {t('mcp.retry')}
          </Button>
        </div>
      )}

      {file && (
        <textarea
          data-testid="mcp-editor-content"
          aria-label={t('mcp.editor.title', { layer: t(`mcp.layer.${layer}`) })}
          value={draft}
          rows={10}
          spellCheck={false}
          onChange={(event) => handleChange(event.target.value)}
          className="mb-3 w-full resize-y rounded-md border border-border-strong bg-background-secondary px-3 py-2 font-mono text-xs leading-5 text-text-primary placeholder:text-text-muted focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20"
        />
      )}

      {validateError && (
        <ErrorBanner testid="mcp-editor-validate-request-error" message={validateError} />
      )}
      {validateFatal && (
        <div className="mb-3">
          <ErrorBanner testid="mcp-editor-validate-fatal" message={validateFatal} />
        </div>
      )}
      {validateWarnings.length > 0 && (
        <div className="mb-3">
          <WarningList
            testid="mcp-editor-validate-warnings"
            title={t('mcp.editor.warningsLabel')}
            items={validateWarnings}
          />
        </div>
      )}
      {validation?.valid && (
        <p data-testid="mcp-editor-validate-ok" className="mb-3 text-xs text-state-success">
          {t('mcp.editor.valid')}
        </p>
      )}

      {saveError && (
        <div className="mb-3">
          <ErrorBanner
            testid="mcp-editor-save-error"
            message={saveError.message}
            code={saveError.code}
          />
        </div>
      )}
      {savedOk && (
        <div className="mb-3">
          <p data-testid="mcp-editor-saved" className="mb-1 text-xs text-state-success">
            {t('mcp.editor.saved')}
          </p>
          {savedWarnings.length > 0 && (
            <WarningList
              testid="mcp-editor-save-warnings"
              title={t('mcp.editor.warningsLabel')}
              items={savedWarnings}
            />
          )}
        </div>
      )}

      <div className="flex items-center justify-end gap-2 pt-1">
        <Button variant="ghost" size="md" data-testid="mcp-editor-close" onClick={onClose}>
          {t('mcp.editor.close')}
        </Button>
        <Button
          variant="secondary"
          size="md"
          data-testid="mcp-editor-validate"
          disabled={validate.isPending}
          onClick={handleValidate}
        >
          {t('mcp.editor.validate')}
        </Button>
        <Button
          variant="primary"
          size="md"
          data-testid="mcp-editor-save"
          disabled={!file || save.isPending}
          loading={save.isPending}
          onClick={handleSave}
        >
          {t('mcp.editor.save')}
        </Button>
      </div>
    </div>
  )
}
