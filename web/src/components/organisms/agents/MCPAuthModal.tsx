/**
 * MCPAuthModal — the OAuth sign-in of ONE MCP server, driven from the MCP tab.
 *
 * WHAT IT MAY RENDER — hard rule, mirroring the frozen `mcpTypes.ts` header:
 * the authorization URL, booleans, fixed state strings and the SERVER's own
 * error message. Nothing else. No token, no client id, no client secret, no
 * authorization code, no PKCE verifier ever reaches this file (the API is
 * shaped so it cannot — `pkg/channels/rest_mcp.go`) and no field here may grow
 * one. `auth_url` is the single URL-shaped value on screen and it is a URL the
 * BROWSER must open: it carries a PKCE *challenge* (a hash, useless without the
 * verifier the gateway keeps) plus `state`, not a secret. A pasted value is
 * forwarded to the server and never rendered back.
 *
 * STATE MACHINE — every phase is DERIVED from the two queries (`start`, `flow`)
 * by `mcpAuthPhase`; the component stores no phase of its own, so a poll
 * answer, a retry or an unmount cannot leave the UI describing a flow that is
 * no longer the one being watched:
 *
 *   starting ──start 200──▶ pending ──poll──▶ done        (credential stored)
 *      │                       │    └─poll──▶ error       (paste box stays: see WHAT RENDERS WHEN)
 *      │                       └─poll 404──▶ expired ──retry──▶ starting
 *      └──start fails──▶ start-failed ──retry──▶ starting
 *                          ├─501 mcp_auth_not_supported──▶ not-supported  (terminal)
 *                          ├─409 mcp_auth_not_required───▶ not-required   (terminal)
 *                          └─503 mcp_auth_disabled───────▶ store-disabled (terminal)
 *
 * WHAT RENDERS WHEN (why the terminal phases have no paste box): `not-supported`
 * / `not-required` mean the server will never accept a code from here, and
 * `store-disabled` means the gateway has no lele directory to persist a login
 * in — the sign-in cannot START, let alone finish — so the manual path is not
 * offered at all and there is nothing to retry. Note `store-disabled` is keyed
 * on the machine CODE, not on its 503: `mcp_unavailable` is a 503 too and IS
 * retryable, so a status-only rule would strand the user on either one.
 * The box IS offered while `pending` and
 * while `error`: the paste is the only way to finish a login from a remote
 * browser, and `error` is AMBIGUOUS on purpose (`mcp-oauth-rest-contract.md`,
 * "error is AMBIGUOUS on purpose") — a failed exchange stays OPEN and
 * retryable, but a failure that arrived through the loopback CALLBACK (a denial
 * redirect, `?error=access_denied`) makes the flow TERMINAL while still being
 * reported as `state: "error"`. The DTO carries no field that tells the two
 * apart, so the box stays armed in both cases; a retry of an already-terminal
 * flow is answered with the same failure again (502 `mcp_auth_failed`,
 * surfaced inline), which is why the client must tolerate a repeated failure
 * rather than read it as "the flow moved on".
 *
 * CLOSING: the X of the atom, `Escape` and the footer button all call
 * `handleClose`. While a flow is UNFINISHED that cancels it server-side (the
 * registry then closes the loopback listener the gateway opened for it): the
 * `pending` phase, and the `error` phase too — a `"error"` does NOT imply the
 * flow is over, so it may still hold its listener until the 5 min deadline.
 * A `done` flow is NOT cancelled: the credential is already stored and the flow
 * holds nothing left to release. (`AuthFlow.Cancel()` on a finished flow is a
 * no-op — pkg/mcp's `finish` is one-shot — so cancelling it would destroy
 * nothing; it would just be a request with no work to do. Closing while the
 * very first POST is still in flight cannot cancel anything either: the flow id
 * is not known yet. The server's "one live flow per (agent, server)" rule reaps
 * that one — the next start for the same pair cancels it — and the 5 min
 * deadline closes it regardless.)
 */
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
  /** Agent whose server is being authenticated — every request is keyed with it. */
  agentId: string
  /** The server NAME whose flow this modal drives (exactly one). */
  serverName: string
  open: boolean
  onClose: () => void
}

/** Every state the modal can be in (see the state machine in the header). */
export type MCPAuthPhase =
  | 'starting'
  | 'start-failed'
  | 'not-supported'
  | 'not-required'
  /** 503 `mcp_auth_disabled`: no lele directory to persist a login in. Terminal. */
  | 'store-disabled'
  | 'pending'
  | 'done'
  | 'error'
  | 'expired'

/**
 * The ONE terminal sentinel the frozen REST contract fixes for a flow that
 * ended in `error`: `mcp.ErrAuthCanceled` is reported with `Error == "canceled"`
 * exactly. `MCPAuthStatus.error` is otherwise the server's own (value-free)
 * message, so only this exact string is translated — see `flowFailureText`.
 */
const CANCELED_SENTINEL = 'canceled'

/** Machine code + HTTP status of a failed call, whichever the client sent. */
function failureOf(error: unknown): { code?: string; status?: number } {
  return {
    code: mcpErrorCode(error),
    status: error instanceof ApiError ? error.status : undefined,
  }
}

/**
 * The modal's phase, DERIVED (never stored).
 *
 * The status code is checked next to the machine code because the two say the
 * same thing in different layers: `501`/`409` from the start route are the
 * "no sign-in possible" verdict the user must be told about, and `404` on the
 * poll is the swept/expired flow. A gateway that answers the code but not the
 * code-string (or vice versa) still lands in the right phase.
 *
 * `mcp_auth_disabled` (503, "no lele directory to persist the login in") is
 * TERMINAL like the two above — and like the TUI and the CLI, which both give
 * it a sentence of its own and offer no retry — so it must NOT fall through to
 * `start-failed`, whose Retry button could never succeed. It is matched on the
 * CODE only: a plain 503 is `mcp_unavailable`, which a gateway restart fixes.
 */
export function mcpAuthPhase(input: {
  /** `start.error` once the start failed, else undefined. */
  startError: unknown
  /** A start response has been kept (its `flow_id` is held). */
  started: boolean
  /** `flow.error` once the poll failed, else undefined. */
  flowError: unknown
  /** `flow.data.state` once a poll answered, else undefined. */
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
    // Swept, cancelled or unknown flow: the id no longer resolves and the user
    // has to start over (the backend's lazy sweep is why this is a normal path).
    return code === 'mcp_auth_flow_not_found' || status === 404 ? 'expired' : 'error'
  }
  if (flowState === 'done') return 'done'
  if (flowState === 'error') return 'error'
  // No answer yet (first poll in flight) reads as pending: the flow IS running.
  return 'pending'
}

/**
 * Inline failure, house idiom: `role="alert"` block, the server's own words
 * and the machine code as a mono chip — never a toast.
 */
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
  /** The flow being watched. Held from the start response, replaced on a retry. */
  const [flowId, setFlowId] = useState<string | null>(null)
  const flow = useMCPAuthFlow(agentId, flowId)
  // The mutations below must be created unconditionally (rules of hooks) and
  // only ever RUN once a flow id exists — every control that calls them is
  // rendered from `flowId !== null` on. '' is the "no flow" placeholder.
  const flowKey = flowId ?? ''
  const complete = useMCPAuthComplete(agentId, flowKey)
  const cancel = useMCPAuthCancel(agentId, flowKey)
  const [paste, setPaste] = useState('')
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const pasteId = useId()

  /**
   * START EXACTLY ONCE, per mount. The effect below must survive re-runs:
   * `beginFlow` is rebuilt whenever the mutation result object changes (i.e.
   * every render), so any re-render between the mount and the start response —
   * including React's STRICT-mode double effect — would fire a SECOND POST.
   * A second start is not free server-side: the registry CANCELS the previous
   * flow for the (agent, server) pair, which would kill the very URL the user
   * is looking at. This ref is the guard; the section keys the component by
   * server name, so another server (or a reopen) gets a fresh one.
   */
  const startedRef = useRef(false)

  /**
   * Start a flow for this server (first open AND retry share it). The old
   * `flow_id` is dropped FIRST: the poller addresses `flowKey`, so clearing it
   * stops the poll on the flow being abandoned and lets the phase fall back to
   * `starting` while the new one is created.
   *
   * No `start.reset()` is needed for the previous outcome: react-query
   * `mutate()` builds a fresh mutation, so the old response (`start.data`, the
   * URL block) and the old failure (`start.error`, the `start-failed` phase)
   * are gone the moment the retry is issued — which is what lets the retry
   * leave `start-failed`/`expired` for `starting`.
   */
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
  /** `error` on the poll: the server's rendered message, or the request message. */
  const flowMessage =
    flow.data?.state === 'error'
      ? (flow.data.error ?? '')
      : flow.isError && flow.error instanceof Error
        ? flow.error.message
        : ''

  /**
   * The paragraph of a flow that landed in `error`.
   *
   * The frozen REST contract fixes exactly ONE terminal sentinel here: a flow
   * that ends in `mcp.ErrAuthCanceled` reports `error == "canceled"`. That is
   * the user's own doing, not a failure of the authorization, so it gets a
   * dedicated localized sentence instead of "The authorization failed:
   * canceled".
   *
   * The TIMEOUT text is deliberately NOT matched: the contract does not freeze
   * it (it is whatever the engine wrote into the flow), so any string test
   * would be a guess that breaks the day the message is reworded — the server's
   * own value-free text is rendered instead. The swept/expired path needs no
   * prose match either: it is the separate `expired` phase (404
   * `mcp_auth_flow_not_found`).
   */
  const flowFailureText =
    flow.data?.state === 'error' && flow.data.error === CANCELED_SENTINEL
      ? t('mcp.auth.canceled')
      : t('mcp.auth.failed', { error: flowMessage })

  /**
   * Release the flow unless it is DONE — see CLOSING in the header. `error`
   * is cancelled too: a terminal `"error"` holds no listener, but a retryable
   * one does, and the DTO cannot tell them apart; the TUI cancels on every
   * exit for the same reason.
   */
  const handleClose = () => {
    if ((phase === 'pending' || phase === 'error') && flowId !== null) cancel.mutate()
    onClose()
  }

  /**
   * Copy the authorization URL. It is NOT a secret (see the header), but it is
   * long and clipboard access can genuinely be missing (plain http, no focus),
   * so the failure is a STATE the user sees instead of a silent no-op: the URL
   * field is select-all, so copying by hand still works.
   */
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
      {/* `data-phase` is the whole contract of this component in one attribute:
          the phase is derived, so tests (and bug reports) can pin it exactly. */}
      <div data-testid="mcp-auth-modal" data-phase={phase} className="flex flex-col gap-4 p-6">
        {showIntro && (
          <div className="flex flex-col gap-1">
            <p data-testid="mcp-auth-intro" className="text-xs text-text-secondary">
              {t('mcp.auth.modalIntro')}
            </p>
            {/* The GATEWAY never opens a browser (headless/remote is the norm);
                the user opens this one on their own machine. */}
            <p data-testid="mcp-auth-remote" className="text-2xs text-text-tertiary">
              {t('mcp.auth.remoteNote')}
            </p>
          </div>
        )}

        {/* ---------- the authorization URL: read-only, selectable, copyable ---------- */}
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
                // Focusable so the field can be reached and select-all'd with
                // the keyboard — the copy button is a convenience, not the only
                // way out of a clipboard-less browser.
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
              {/* House external-link convention: a new tab, no opener handle. */}
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

        {/* ---------- derived status line (never colour-only) ---------- */}
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

        {/* ---------- the start itself failed: retry (unless terminal) ---------- */}
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

        {/* ---------- terminal verdicts: no sign-in possible from here ---------- */}
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
        {/* 503 `mcp_auth_disabled`: the gateway has nowhere to persist a login
            in. Terminal, and the sentence is fixed i18n text — the server's own
            message is not echoed here. */}
        {phase === 'store-disabled' && (
          <p data-testid="mcp-auth-store-disabled" className="text-xs text-text-secondary">
            {t('mcp.auth.storeDisabled')}
          </p>
        )}

        {/* ---------- the flow died server-side (swept/cancelled): start again ---------- */}
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

        {/* ---------- the exchange failed: the paste box below is the retry ---------- */}
        {phase === 'error' && (
          <Failure
            testId="mcp-auth-error"
            message={flowFailureText}
            code={flow.isError ? mcpErrorCode(flow.error) : undefined}
          />
        )}

        {/* ---------- manual completion (remote browser, headless gateway) ---------- */}
        {showManual && (
          <div
            data-testid="mcp-auth-manual"
            className="flex flex-col gap-2 rounded-lg border border-border bg-background-secondary/60 p-3"
          >
            {/* Visible prose, not a tooltip: it is the only place the "the
                callback cannot reach this machine" rule is explained. */}
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
            {/* A refusal pasted in: surfaced as a failure, never submitted.
                The `error` code is echoed ONLY when the parser's sanitizer
                (the mirror of the server-side rule) accepted it; a hostile or
                merely odd value leaves it absent and the fixed sentence below
                is shown instead — no server byte reaches this paragraph. */}
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
                // Only a parsed `code` can be submitted; an empty or garbage box
                // keeps the control disabled and never reaches the server.
                disabled={parsed.kind !== 'code'}
                loading={complete.isPending}
                onClick={submitPaste}
              >
                {t('mcp.auth.submit')}
              </Button>
            </div>
          </div>
        )}

        {/* ---------- one footer control: Cancel while the flow is pending,
            Close otherwise. The `error` phase names the button Close (the
            request failed, so there is nothing to "cancel" from the user's
            point of view) but still releases an unfinished flow on the way out
            — see handleClose. ---------- */}
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
