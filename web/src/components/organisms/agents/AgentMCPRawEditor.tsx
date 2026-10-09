/**
 * AgentMCPRawEditor — the per-layer raw mcp.json editor of the MCP tab.
 *
 * One instance hangs off the layer row the user clicked (mounted by
 * AgentMCPSection with `key={layer}`, so its mutation state never leaks
 * between layers). Structure and controls follow the house idiom the Commands
 * tab already uses (`AgentCommandEditorDialog`): a monospace textarea holding
 * the FILE BYTES verbatim, a visible path line, an inline `role="alert"`
 * banner for every backend rejection (the server's own words, never a
 * generic toast), and a Cancel/…/Save footer.
 *
 * Two rules this component must keep:
 *
 *  - NO SECRET EXPANSION. `content` is the literal file bytes with `${VAR}`
 *    never expanded — that is the design, and the textarea shows it as-is.
 *    There is deliberately no control that requests expanded values (no such
 *    endpoint exists) and none may be invented here.
 *  - WARNINGS NEVER BLOCK A SAVE. A per-entry problem is a 200 with
 *    `warnings[]`; only a fatal envelope problem is a 422 `config_invalid`.
 *    So Validate is always reachable (it is how a broken file gets repaired),
 *    Save is disabled only while a request is in flight, and warnings —
 *    from Validate or from the Save response itself — render AFTER the
 *    document is already on disk.
 *
 * Dirty tracking lives in the SECTION (it must survive this component's
 * remount when the user switches layers), so the draft/baseline pair and the
 * close/switch gating are wired through props; this component only reports
 * intent (`onClose`, `onDraftChange`) and renders the inline unsaved-changes
 * warning the section holds back (`nav`).
 */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { mcpErrorCode, useMCPValidate, useSaveMCPRaw } from '../../../hooks/useAgentMCP'
import type { MCPLayerName, MCPRawFileResponse, MCPValidateResponse } from '../../../lib/mcpTypes'
import { Button } from '../../atoms/Button'

/**
 * Navigation the section is holding back because the draft is dirty: the
 * inline warning asks for an explicit discard before anything is thrown away.
 */
export type MCPRawNav = { kind: 'close' } | { kind: 'switch'; target: MCPLayerName }

type Props = {
  agentId: string
  /** The open layer (the section already filtered to a stored layer). */
  layer: MCPLayerName
  /** Raw GET payload of THAT layer once loaded (undefined while loading). */
  file: MCPRawFileResponse | undefined
  loading: boolean
  loadFailed: boolean
  onRetryLoad: () => void
  /** Current draft bytes (section-owned state). */
  draft: string
  /** Human label of any stored layer — for the aliasing warning. */
  layerLabel: (layer: string) => string
  /** Pending unsaved-changes navigation, or null. */
  nav: MCPRawNav | null
  onDraftChange: (content: string) => void
  /** Save succeeded with the bytes actually on disk (response `content`). */
  onSaved: (content: string) => void
  onClose: () => void
  onConfirmNav: () => void
  onDismissNav: () => void
}

/** Inline server-message banner (commands dialog `ServerError`, same idiom). */
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

/** One warning list: validate warnings and post-save warnings share it. */
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

  /**
   * Validate the DRAFT (what the user is looking at), never the file on
   * disk: the whole point is repairing a document the server would reject.
   */
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
        // `data.content` is what is on disk: sync the draft/baseline so the
        // textarea shows the SAVED bytes and the editor goes clean, even
        // before the invalidated raw query refetches.
        setSavedOk(true)
        setSavedWarnings(data.warnings ?? [])
        onSaved(data.content)
      },
      onError: (error) => {
        // 422 config_invalid / 403 mcp_path_not_allowed / 400 mcp_layer_unavailable
        // / 400 body_invalid / 413: the server's message is the useful part.
        // The draft is NOT touched — dirty state survives a failed save.
        setSavedOk(false)
        setSavedWarnings([])
        setSaveError({
          message: error instanceof Error ? error.message : String(error),
          code: mcpErrorCode(error),
        })
      },
    })
  }

  /** A keystroke invalidates every result computed from older bytes. */
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

      {/* ---------- inline unsaved-changes warning (never a modal) ---------- */}
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

      {/* ---------- path line ---------- */}
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

      {/* ---------- aliasing: editing THIS file edits those layers too ---------- */}
      {aliased.length > 0 && (
        <div
          role="alert"
          data-testid="mcp-editor-aliased"
          className="mb-3 rounded-lg border border-state-warning/50 bg-state-warning/15 px-3 py-2 text-xs font-medium text-state-warning"
        >
          {t('mcp.editor.aliased', { layers: aliased.map(layerLabel).join(', ') })}
        </div>
      )}

      {/* ---------- load states ---------- */}
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

      {/* ---------- the document: literal bytes, never expanded ---------- */}
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

      {/* ---------- validate result: fatal vs. warnings, shown distinctly ---------- */}
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

      {/* ---------- save result ---------- */}
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

      {/* ---------- footer (commands dialog idiom: Close · Validate · Save) ---------- */}
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
