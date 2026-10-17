/**
 * The server's send-refusal event must reach the user.
 *
 * The backend refuses a send with `sendError(client, code, message)`
 * (`pkg/channels/websocket.go`), i.e. `{"event":"error","data":{"code",
 * "message"}}`. `services/ws/events.ts` had no handler for it, so EVERY
 * refusal was dropped: a user could press send on a turn the server rejected
 * (no text and no attachment, or attachments that could not be read) and see
 * absolutely nothing happen. These tests pin the dispatcher entry AND the
 * code → i18n mapping, because a mapped key that nobody renders is still a
 * silent failure.
 *
 * The socket-level failure is a DIFFERENT channel (the DOM `error` event is
 * re-emitted on the client object by `services/ws/client.ts` and nobody
 * subscribes to it), so only parsed envelopes can reach `handleServerError` —
 * there is no payload sniffing to get wrong.
 *
 * The code→key table is pinned BOTH ways since the post-review fix (MENOR-2):
 * the locale test pins what the table renders, and the last test parses the
 * producer (`sendError(client, "…")` in pkg/channels/websocket.go) so a row
 * could not be mapped for a code this transport never emits — which is exactly
 * how `access_denied` (HTTP-only) sat here while `forbidden` went untranslated.
 *
 * Mutation table (PLAN §5.5-D) — applied one at a time and reverted:
 *
 *   M20  drop the `error` row from the HANDLERS table
 *        -> RED: "M20: the event is dispatched and reaches setError"
 *   M21  `if (!token) return []` in handleUploadAttachments (reverted)
 *        -> RED: sendFailureVisibility.test.ts > M21 (silent refusal)
 *   M22  AttachmentInput back to `paths.length === 0` (total failure only)
 *        -> RED: AttachmentInput.test.tsx > partial (3 requested, 2 returned)
 *
 * All three went green again after the reverts.
 */
import '../../test/setup'
import '../../test/i18n'

import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import i18n from '../../i18n'
import { SEND_ERROR_I18N_KEYS, sendErrorMessage } from '../../lib/sendErrors'
import { dispatchMessageEvent } from './index'
import type { MessageEventContext } from './types'

/**
 * The WS producer of every code this table can meet: `sendError(client, code,
 * message)` (pkg/channels/websocket.go). Read from source because the mapping is
 * only correct relative to what the transport emits — see the source pin below.
 */
const WEBSOCKET_GO = join(import.meta.dir, '../../../../pkg/channels/websocket.go')

function makeCtx() {
  const errors: string[] = []
  const ctx = {
    currentSessionKeyRef: { current: 'native:s1' },
    parentSessionKeyRef: { current: null },
    // The server-error handler reads the streaming list to retract an
    // attachments-only bubble refused by the server (MENOR-1) and writes both
    // back. Empty here: these tests pin the banner, not the retraction.
    streamingMessagesRef: { current: [] },
    setStreamingMessages: () => {},
    setPendingAttachments: () => {},
    setError: (message: string) => errors.push(message),
    setTypingIndicator: () => {},
  } as unknown as MessageEventContext
  return { ctx, errors }
}

describe('dispatchMessageEvent — server `error` event', () => {
  test('M20: the event is dispatched and reaches setError (the banner)', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, {
      event: 'error',
      data: { code: 'content_missing', message: 'message content or an attachment is required' },
    })

    // Without the HANDLERS entry this list stays empty: M20 (drop the dispatch
    // row) is exactly the regression this assertion catches.
    expect(errors.length).toBe(1)
    expect(errors[0]).toBe(i18n.t('chat.errorContentMissing'))
    // …and NOT the raw server prose: the code is the stable half of the pair.
    expect(errors[0]).not.toContain('message content or an attachment')
  })

  test('the post-processing refusal (attachments_rejected) is surfaced too', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, {
      event: 'error',
      data: {
        code: 'attachments_rejected',
        message: 'attachments could not be read; add text or re-upload',
      },
    })

    expect(errors).toEqual([i18n.t('chat.errorAttachmentsRejected')])
  })

  test('the session refusal (`forbidden`, the code the WS really sends) is translated', () => {
    const { ctx, errors } = makeCtx()

    // MENOR-2: the table used to map `access_denied` — an HTTP-only code — and
    // leave `forbidden` (websocket.go:384/517) to the English server prose, so
    // an es/pt user read "access denied to this session" untranslated.
    dispatchMessageEvent(ctx, {
      event: 'error',
      data: { code: 'forbidden', message: 'access denied to this session' },
    })

    expect(errors).toEqual([i18n.t('chat.errorAccessDenied')])
    expect(errors[0]).not.toContain('access denied')
  })

  test('an unrelated event still does not touch the error banner', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, { event: 'typing.indicator', data: { session_key: 'native:s1' } })

    expect(errors).toEqual([])
  })

  test('an event with no data at all does not throw', () => {
    const { ctx, errors } = makeCtx()

    dispatchMessageEvent(ctx, { event: 'error', data: undefined })

    expect(errors).toEqual([i18n.t('chat.failedToSend')])
  })
})

describe('sendErrorMessage — code → text', () => {
  test('every mapped code has a real translation in all three locales', () => {
    const codes = Object.keys(SEND_ERROR_I18N_KEYS)
    expect(codes.sort()).toEqual(['attachments_rejected', 'content_missing', 'forbidden'])

    for (const locale of ['en', 'es', 'pt']) {
      for (const code of codes) {
        const text = i18n.getFixedT(locale)(SEND_ERROR_I18N_KEYS[code])
        // A missing key comes back as the key itself — that would render as
        // "chat.errorContentMissing" in the banner.
        expect(text).not.toBe(SEND_ERROR_I18N_KEYS[code])
        expect((text as string).length).toBeGreaterThan(0)
      }
    }
  })

  test('each code maps to its own key (no copy-paste collisions)', () => {
    const keys = Object.values(SEND_ERROR_I18N_KEYS)
    expect(new Set(keys).size).toBe(keys.length)
  })

  test('an unknown code falls back to the server message, then to the generic key', () => {
    // A newer backend with a code this build does not know: say what it said.
    expect(sendErrorMessage('brand_new_code', 'The server says no')).toBe('The server says no')
    // Nothing usable at all: still say SOMETHING.
    expect(sendErrorMessage('', '')).toBe(i18n.t('chat.failedToSend'))
    expect(sendErrorMessage('brand_new_code')).toBe(i18n.t('chat.failedToSend'))
  })

  test('the table only maps codes the WS producer really sends (source pin)', () => {
    // MENOR-2's root cause was a table asserted against ITSELF: it was green
    // while mapping `access_denied`, a code the WebSocket never emits
    // (pkg/channels/upload.go answers it over HTTP). Pin the producer instead:
    // every code mapped here must appear in a `sendError(client, "...")` call of
    // pkg/channels/websocket.go, and `forbidden` must be one of them.
    const source = readFileSync(WEBSOCKET_GO, 'utf8')
    const produced = new Set(
      [...source.matchAll(/sendError\(client,\s*"([a-z_]+)"/g)].map((match) => match[1]),
    )

    // Sanity: the parse found the producer at all (a renamed helper must not
    // turn this pin into a silent pass).
    expect(produced.has('forbidden')).toBe(true)
    expect(produced.has('content_missing')).toBe(true)
    expect(produced.has('attachments_rejected')).toBe(true)

    for (const code of Object.keys(SEND_ERROR_I18N_KEYS)) {
      expect(produced.has(code)).toBe(true)
    }
  })
})
