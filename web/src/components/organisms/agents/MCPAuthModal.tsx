import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  mcpErrorCode,
  useMCPAuthCancel,
  useMCPAuthComplete,
  useMCPAuthFlow,
  useMCPAuthStart,
} from '../../../hooks/useAgentMCP'
import { parseMCPAuthPaste } from '../../../lib/mcpAuthPaste'
import { ApiError } from '../../../services/http/errors'
import { Button } from '../../atoms/Button'
import { Modal } from '../../atoms/Modal'
import { Spinner } from '../../atoms/Spinner'

type Props = {
  agentId: string
  serverName: string
  open: boolean
  onClose: () => void
}

export type MCPAuthPhase =
  | 'starting'
  | 'start-failed'
  | 'not-supported'
  | 'not-required'
  | 'store-disabled'
  | 'pending'
  | 'done'
  | 'error'
  | 'expired'

const CANCELED_SENTINEL = 'canceled'

function failureOf(error: unknown): { code?: string; status?: number } {
  return {
    code: mcpErrorCode(error),
    status: error instanceof ApiError ? error.status : undefined,
  }
}

export function mcpAuthPhase(input: {
  startError: unknown
  started: boolean
  flowError: unknown
  flowState: string | undefined
}): MCPAuthPhase {
  const { startError, started, flowError, flowState } = input

  if (startError !== undefined) {
    const { code, status } = failureOf(startError)
    if (code === 'mcp_auth_not_supported' || status === 501) return 'not-supported'
    if (code === 'mcp_auth_not_required' || status === 409) return 'not-required'
    if (code === 'mcp_auth_disabled') return 'store-disabled'
    return 'start-failed'
  }
  if (!started) return 'starting'
  if (flowError !== undefined) {
    const { code, status } = failureOf(flowError)
    return code === 'mcp_auth_flow_not_found' || status === 404 ? 'expired' : 'error'
  }
  if (flowState === 'done') return 'done'
  if (flowState === 'error') return 'error'
  return 'pending'
}

function Failure({ testId, message, code }: { testId: string; message: string; code?: string }) {
  return (
    <div
      role="alert"
      data-testid={testId}
      className="flex flex-wrap items-center gap-2 rounded-lg border border-state-error/40 bg-state-error/10 px-3 py-2 text-xs text-state-error"
    >
      <span className="min-w-0 flex-1 break-all">{message}</span>
      {code !== undefined && <span className="font-mono text-2xs">{code}</span>}
    </div>
  )
}

export function MCPAuthModal({ agentId, serverName, open, onClose }: Props) {
  const { t } = useTranslation()
  const start = useMCPAuthStart(agentId)
  const [flowId, setFlowId] = useState<string | null>(null)
  const flow = useMCPAuthFlow(agentId, flowId)
  const flowKey = flowId ?? ''
  const complete = useMCPAuthComplete(agentId, flowKey)
  const cancel = useMCPAuthCancel(agentId, flowKey)
  const [paste, setPaste] = useState('')
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const pasteId = useId()

  const startedRef = useRef(false)

  const beginFlow = () => {
    setFlowId(null)
    setPaste('')
    setCopyState('idle')
    start.mutate(serverName, { onSuccess: (auth) => setFlowId(auth.flow_id) })
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: `open` is the trigger — `beginFlow` is rebuilt on every render and re-running it is exactly what the ref above exists to absorb; a dependency on it would only add renders, not starts.
  useEffect(() => {
    if (!open || startedRef.current) return
    startedRef.current = true
    beginFlow()
  }, [open])

  const phase = mcpAuthPhase({
    startError: start.isError ? start.error : undefined,
    started: flowId !== null,
    flowError: flow.isError ? flow.error : undefined,
    flowState: flow.data?.state,
  })

  const authUrl = start.data?.auth_url ?? ''
  const startFailure = failureOf(start.error)
  const flowMessage =
    flow.data?.state === 'error'
      ? (flow.data.error ?? '')
      : flow.isError && flow.error instanceof Error
        ? flow.error.message
        : ''

  const flowFailureText =
    flow.data?.state === 'error' && flow.data.error === CANCELED_SENTINEL
      ? t('mcp.auth.canceled')
      : t('mcp.auth.failed', { error: flowMessage })

  const handleClose = () => {
    if ((phase === 'pending' || phase === 'error') && flowId !== null) cancel.mutate()
    onClose()
  }

  const copyAuthUrl = async () => {
    try {
      await navigator.clipboard.writeText(authUrl)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  const parsed = parseMCPAuthPaste(paste)
  const submitPaste = () => {
    if (parsed.kind !== 'code' || flowId === null) return
    complete.mutate(
      parsed.state === undefined
        ? { code: parsed.code }
        : { code: parsed.code, state: parsed.state },
    )
  }

  const showIntro =
    phase !== 'not-supported' && phase !== 'not-required' && phase !== 'store-disabled'
  const showManual = phase === 'pending' || phase === 'error'

  return (
    <Modal
      isOpen={open}
      onClose={handleClose}
      title={t('mcp.auth.modalTitle', { name: serverName })}
      size="md"
    >
      <div data-testid="mcp-auth-modal" data-phase={phase} className="flex flex-col gap-4 p-6">
        {showIntro && (
          <div className="flex flex-col gap-1">
            <p data-testid="mcp-auth-intro" className="text-xs text-text-secondary">
              {t('mcp.auth.modalIntro')}
            </p>
            <p data-testid="mcp-auth-remote" className="text-2xs text-text-tertiary">
              {t('mcp.auth.remoteNote')}
            </p>
          </div>
        )}

        {authUrl !== '' && (
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-text-secondary">
              {t('mcp.auth.urlLabel')}
            </span>
            <div className="flex items-center gap-2">
              {/* biome-ignore lint/a11y/useSemanticElements: the field must WRAP a long authorization URL (a native <input> clips it to one line) while staying a read-only, select-all textbox — same read-only-field idiom as the agent ID field in AgentGeneralSection */}
              <div
                role="textbox"
                aria-readonly="true"
                aria-label={t('mcp.auth.urlLabel')}
                tabIndex={0}
                data-testid="mcp-auth-url"
                title={authUrl}
                className="min-w-0 flex-1 select-all break-all rounded-md border border-border bg-background-tertiary px-3 py-2 font-mono text-xs text-text-secondary"
              >
                {authUrl}
              </div>
              <Button
                variant="secondary"
                size="sm"
                data-testid="mcp-auth-copy"
                aria-label={t('mcp.auth.copyUrl')}
                onClick={() => void copyAuthUrl()}
              >
                {t('mcp.auth.copyUrl')}
              </Button>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <a
                href={authUrl}
                target="_blank"
                rel="noopener noreferrer"
                data-testid="mcp-auth-open"
                aria-label={t('mcp.auth.openInBrowserAria', { name: serverName })}
                className="text-xs text-focus underline decoration-border underline-offset-2 hover:text-focus-hover"
              >
                {t('mcp.auth.openInBrowser')}
              </a>
              {copyState === 'copied' && (
                <span data-testid="mcp-auth-copy-status" className="text-2xs text-state-success">
                  {t('mcp.auth.copied')}
                </span>
              )}
              {copyState === 'failed' && (
                <span data-testid="mcp-auth-copy-status" className="text-2xs text-state-error">
                  {t('mcp.auth.copyFailed')}
                </span>
              )}
            </div>
          </div>
        )}

        {(phase === 'starting' || phase === 'pending' || phase === 'done') && (
          <p
            data-testid="mcp-auth-status"
            className={`flex items-center gap-2 text-xs ${
              phase === 'done' ? 'text-state-success' : 'text-text-secondary'
            }`}
          >
            {phase === 'done' ? (
              t('mcp.auth.done')
            ) : (
              <>
                <Spinner size="sm" />
                {t('mcp.auth.waiting')}
              </>
            )}
          </p>
        )}

        {phase === 'start-failed' && (
          <div className="flex flex-col gap-2">
            <Failure
              testId="mcp-auth-error"
              message={start.error instanceof Error ? start.error.message : String(start.error)}
              code={startFailure.code}
            />
            <div className="flex justify-end">
              <Button
                variant="secondary"
                size="sm"
                data-testid="mcp-auth-retry"
                onClick={beginFlow}
              >
                {t('mcp.retry')}
              </Button>
            </div>
          </div>
        )}

        {phase === 'not-supported' && (
          <p data-testid="mcp-auth-not-supported" className="text-xs text-text-secondary">
            {t('mcp.auth.notSupported')}
          </p>
        )}
        {phase === 'not-required' && (
          <p data-testid="mcp-auth-not-required" className="text-xs text-text-secondary">
            {t('mcp.auth.notRequired')}
          </p>
        )}
        {phase === 'store-disabled' && (
          <p data-testid="mcp-auth-store-disabled" className="text-xs text-text-secondary">
            {t('mcp.auth.storeDisabled')}
          </p>
        )}

        {phase === 'expired' && (
          <div className="flex flex-col gap-2">
            <p data-testid="mcp-auth-expired" className="text-xs text-text-secondary">
              {t('mcp.auth.expired')}
            </p>
            <div className="flex justify-end">
              <Button
                variant="secondary"
                size="sm"
                data-testid="mcp-auth-retry"
                onClick={beginFlow}
              >
                {t('mcp.retry')}
              </Button>
            </div>
          </div>
        )}

        {phase === 'error' && (
          <Failure
            testId="mcp-auth-error"
            message={flowFailureText}
            code={flow.isError ? mcpErrorCode(flow.error) : undefined}
          />
        )}

        {showManual && (
          <div
            data-testid="mcp-auth-manual"
            className="flex flex-col gap-2 rounded-lg border border-border bg-background-secondary/60 p-3"
          >
            <p data-testid="mcp-auth-manual-note" className="text-xs text-text-secondary">
              {t('mcp.auth.manualNote')}
            </p>
            <label htmlFor={pasteId} className="text-xs font-medium text-text-secondary">
              {t('mcp.auth.pasteLabel')}
            </label>
            <textarea
              id={pasteId}
              data-testid="mcp-auth-paste"
              value={paste}
              onChange={(event) => setPaste(event.target.value)}
              aria-describedby={`${pasteId}-hint`}
              rows={2}
              spellCheck={false}
              className="w-full rounded-md border border-border-strong bg-background-secondary px-3 py-2 font-mono text-xs text-text-primary focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus/20"
            />
            <p
              id={`${pasteId}-hint`}
              data-testid="mcp-auth-paste-hint"
              className="text-2xs text-text-tertiary"
            >
              {t('mcp.auth.pasteHint')}
            </p>
            {parsed.kind === 'denied' && (
              <p
                role="alert"
                data-testid="mcp-auth-paste-error"
                className="text-xs text-state-error"
              >
                {parsed.error === undefined
                  ? t('mcp.auth.refused')
                  : t('mcp.auth.failed', { error: parsed.error })}
              </p>
            )}
            {complete.isError && (
              <Failure
                testId="mcp-auth-complete-error"
                message={
                  complete.error instanceof Error ? complete.error.message : String(complete.error)
                }
                code={mcpErrorCode(complete.error)}
              />
            )}
            <div className="flex justify-end">
              <Button
                variant="primary"
                size="sm"
                data-testid="mcp-auth-submit"
                disabled={parsed.kind !== 'code'}
                loading={complete.isPending}
                onClick={submitPaste}
              >
                {t('mcp.auth.submit')}
              </Button>
            </div>
          </div>
        )}

        <div className="flex justify-end border-t border-border pt-3">
          {phase === 'pending' ? (
            <Button
              variant="secondary"
              size="sm"
              data-testid="mcp-auth-cancel"
              onClick={handleClose}
            >
              {t('mcp.auth.cancel')}
            </Button>
          ) : (
            <Button
              variant="secondary"
              size="sm"
              data-testid="mcp-auth-close"
              onClick={handleClose}
            >
              {t('mcp.auth.close')}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  )
}
