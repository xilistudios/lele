/**
 * Send/upload failures are visible IN THE APP, not just in the dispatcher.
 *
 * Two ways a failure used to disappear without a word:
 *  1. no token: `handleUploadAttachments` returned `[]` immediately, which the
 *     composer reads as a failure but nothing explained it to the user;
 *  2. a refused send: the server's `error` event was dispatched nowhere (see
 *     event-handlers/serverError.test.ts) so the banner stayed empty.
 *
 * Both are asserted through the REAL `useAppLogic`, so the wiring from the
 * socket/upload path to `error` (-> `ChatPage.tsx` `ErrorBanner`) is what is
 * being tested, not a mock.
 */
import '../test/setup'
import '../test/i18n'

import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup } from '@testing-library/react'
import i18n from '../i18n'
import { renderAppLogic, stubApi } from '../test/appLogicHarness'

afterEach(() => cleanup())

describe('useAppLogic — upload failures reach the error banner', () => {
  test('M21: with no token the upload is refused AND explained', async () => {
    // No token also means no agent list (the agents effect is gated on it), so
    // there is nothing to wait for: flush the mount effects and upload.
    const { result } = renderAppLogic({ token: null })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    // The signature must not change: AttachmentInput reads [] as "nothing was
    // attached" (and shows its own message only when it sees no paths at all).
    expect(paths).toEqual([])
    // M21: reverting to a bare `return []` leaves this null — a silent failure.
    expect(result.current.error).toBe(i18n.t('chat.notAuthenticated') as string)
  })

  test('a successful upload says nothing (no false alarm)', async () => {
    const { result, waitUntilReady } = renderAppLogic({
      api: stubApi({ uploadFiles: async () => ({ files: [{ path: '/uploads/a.png' }] }) }),
    })
    await waitUntilReady()

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    expect(paths).toEqual(['/uploads/a.png'])
    expect(result.current.error).toBe(null)
  })

  test('a failing upload surfaces the API message', async () => {
    const { result, waitUntilReady } = renderAppLogic({
      api: stubApi({
        uploadFiles: async () => {
          throw new Error('file too large')
        },
      }),
    })
    await waitUntilReady()

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    expect(paths).toEqual([])
    expect(result.current.error).toBe('file too large')
  })
})

describe('useAppLogic — a refused send is shown to the user', () => {
  test('the server `error` event lands in the banner (end-to-end wiring)', async () => {
    const { result, waitUntilReady, sessionKey } = renderAppLogic()
    await waitUntilReady()

    await act(async () => {
      result.current.handleEvent({
        event: 'error',
        data: {
          code: 'attachments_rejected',
          message: 'attachments could not be read; add text or re-upload',
        },
      })
    })

    expect(result.current.error).toBe(i18n.t('chat.errorAttachmentsRejected') as string)
    // The refusal is scoped to the send: it must not stop the session.
    expect(result.current.isProcessing).toBe(false)
    expect(sessionKey.length).toBeGreaterThan(0)
  })
})

// ── MENOR-1: a refusal of an attachments-only send gives the turn back ───────

const SESSION_KEY = 'native:attach-refused'
/** Client-side staging path of a pending upload (the server persists another). */
const STAGING = '/lele/tmp/uploads/ab12cd34_photo.jpg'

/**
 * `attachments_rejected` is produced by the POST-processing guard, which only
 * fires for a turn with NO text whose every attachment was dropped
 * (pkg/channels/websocket.go). So the pair of side effects below belongs to
 * exactly that turn: the optimistic bubble would otherwise live forever (no
 * persisted copy will ever confirm it — see `isAttachmentsOnlyBase`) and the
 * chips cleared on local acceptance would be lost for good.
 */
describe('useAppLogic — the server refuses an attachments-only send', () => {
  test('MENOR-1: the bubble is retracted and the chips carry the paths again', async () => {
    const { result, waitUntilReady } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      result.current.onAttachmentsChange([STAGING])
    })
    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.onSend('', [STAGING])
    })

    // Accepted LOCALLY (what the returned boolean means): the chips are burned
    // and the turn is on screen, with the staging path the client will show.
    expect(accepted).toBe(true)
    expect(result.current.pendingAttachments).toEqual([])
    const userTurns = () => result.current.messages.filter((m) => m.role === 'user')
    expect(userTurns().length).toBe(1)
    expect(userTurns()[0].optimistic).toBe(true)
    expect(userTurns()[0].attachments?.map((a) => a.path)).toEqual([STAGING])

    await act(async () => {
      result.current.handleEvent({
        event: 'error',
        data: {
          code: 'attachments_rejected',
          message: 'attachments could not be read; add text or re-upload',
        },
      })
    })

    // The banner (T4) AND the turn handed back to the composer: no immortal
    // bubble, no re-uploading the same file to retry.
    expect(result.current.error).toBe(i18n.t('chat.errorAttachmentsRejected') as string)
    expect(userTurns()).toEqual([])
    expect(result.current.pendingAttachments).toEqual([STAGING])
  })

  test('MENOR-1: no matching bubble is the banner alone — nothing retracted, no crash', async () => {
    const { result, waitUntilReady } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    // A turn WITH text has its content identity: it is never an
    // attachments-only bubble, so a late/duplicate refusal must leave it alone.
    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.onSend('hello', [])
    })
    expect(accepted).toBe(true)

    const refuse = () =>
      act(async () => {
        result.current.handleEvent({
          event: 'error',
          data: {
            code: 'attachments_rejected',
            message: 'attachments could not be read; add text or re-upload',
          },
        })
      })

    await refuse()
    expect(result.current.error).toBe(i18n.t('chat.errorAttachmentsRejected') as string)
    // Untouched: nothing to hand back, and no text turn was mistaken for one.
    const userTurns = () => result.current.messages.filter((m) => m.role === 'user')
    expect(userTurns().map((m) => m.content)).toEqual(['hello'])
    expect(result.current.pendingAttachments).toEqual([])

    // A second copy of the same refusal (or an error that arrived after the turn
    // was already confirmed) has nothing left to retract either.
    await refuse()
    expect(userTurns().map((m) => m.content)).toEqual(['hello'])
    expect(result.current.pendingAttachments).toEqual([])
  })
})
