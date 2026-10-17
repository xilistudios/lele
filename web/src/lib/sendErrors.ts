/**
 * Server-side send failures → user-visible text.
 *
 * WHY this exists: the three send endpoints reject a payload with a Machine
 * code (`pkg/channels/sendpayload.go` / `websocket.go` → `sendError(client,
 * code, message)`), and the WebUI used to dispatch no handler for that event at
 * all, so every one of those rejections was silent: the message vanished from
 * the composer (or worse, nothing was sent and nothing was said).
 *
 * The code is the STABLE half of the pair — messages are prose and may be
 * reworded or localized server-side — so translation keys are keyed by code,
 * and the server's own message is only the fallback for codes this build does
 * not know yet (a newer backend). The generic key is the last resort.
 *
 * Pure module (no React): the mapping is a table, so it is testable as one and
 * the same table serves the WS event stream today and any REST caller later.
 */
import i18n from '../i18n'

/**
 * i18n key per backend error code, for the codes the WebSocket `error` event can
 * actually carry — every remote refusal a send can meet:
 *
 *  - `content_missing`: the pre-processing guard (no text AND no attachments);
 *  - `attachments_rejected`: the post-processing guard (the turn carried
 *    attachments and every single one was dropped, so an empty turn is not worth
 *    publishing);
 *  - `forbidden`: "access denied to this session", the code of BOTH the
 *    `message` and the `subscribe` paths in pkg/channels/websocket.go, and the
 *    code the reviewer found missing here while two HTTP-only codes were mapped.
 *
 * Deliberately NOT listed: `access_denied` and `file_too_large`. Both are
 * emitted over HTTP only (`pkg/channels/upload.go`: the upload's 413 and the
 * `/api/v1/files/view` viewer), and the only consumer of this table is the WS
 * `error` handler, so as rows they were unreachable by construction — the
 * "mapped key that nobody renders" trap of the original PR. `forbidden` reuses
 * their translation (`chat.errorAccessDenied`); `chat.errorFileTooLarge` stays
 * in the locales, unused, for a REST caller of `sendErrorMessage`.
 *
 * The producer side is pinned by a test in event-handlers/serverError.test.ts
 * (`sendError(...)` call sites, parsed out of pkg/channels/websocket.go), so a
 * code added here must be one the transport really sends.
 */
export const SEND_ERROR_I18N_KEYS: Record<string, string> = {
  content_missing: 'chat.errorContentMissing',
  attachments_rejected: 'chat.errorAttachmentsRejected',
  forbidden: 'chat.errorAccessDenied',
}

/**
 * Unknown code (or none) → the server's own message, so a newer backend still
 * says something specific. Nothing at all → the generic send-failure key.
 */
export function sendErrorMessage(code: string, serverMessage = ''): string {
  const key = SEND_ERROR_I18N_KEYS[code]
  if (key) return i18n.t(key)
  if (serverMessage) return serverMessage
  return i18n.t('chat.failedToSend')
}
