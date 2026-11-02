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
import type { ChatMessage } from '../lib/types'
import { renderAppLogic, stubApi } from '../test/appLogicHarness'
import { dispatchMessageEvent } from './event-handlers/index'
import type { MessageEventContext } from './event-handlers/types'

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

// ── REFUSED-1: every OTHER refusal marks the newest bubble as failed ─────────
//
// A refusal (`{"event":"error","data":{"code","message"}}`,
// pkg/channels/websocket.go `sendError`) answers a send the client already
// accepted locally. Without a `failed` marker the optimistic bubble is
// STRANDED: the only marker code path was `handleMessageError` (the
// `message.error` event, which the backend never emits) and `mergeMessages`
// appends unmatched streaming messages AFTER canonical history, so the bubble
// rendered at the very bottom of the transcript, below newer answers, with no
// retry button (`MessageBubble` only offers one when `message.failed`).
//
// Two layers are asserted:
//  * end-to-end through the REAL `useAppLogic`, for the code the server really
//    sends (`rate_limit_exceeded`);
//  * the bare dispatcher with a stubbed context that applies the streaming
//    update, for the shapes that are hard to stage through the hook (a second
//    rapid send, and a refusal whose key can never reach the UI state).

/** The refusal the backend answers a too-fast send with. */
const RATE_LIMITED = {
  code: 'rate_limit_exceeded',
  message: 'rate limit exceeded, please slow down',
}

describe('useAppLogic — a refused send is no longer stranded (REFUSED-1)', () => {
  test('rate_limit_exceeded marks the bubble failed, so it offers a retry', async () => {
    const { result, waitUntilReady } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      await result.current.onSend('hello', [])
    })
    const userTurns = () => result.current.messages.filter((m) => m.role === 'user')
    // Accepted locally: the bubble is on screen, unmarked and not retryable.
    expect(userTurns().length).toBe(1)
    expect(userTurns()[0].optimistic).toBe(true)
    expect(userTurns()[0].failed).toBeUndefined()

    await act(async () => {
      result.current.handleEvent({ event: 'error', data: RATE_LIMITED })
    })

    // REFUSED-1: without the marker this stays undefined and `MessageBubble`
    // renders neither the "failed to send" label nor the retry button.
    expect(userTurns()[0].failed).toBe(true)
    // The bubble itself stays: it was accepted locally, the user must see WHAT
    // was refused (and retry it), not just a banner.
    expect(userTurns().map((m) => m.content)).toEqual(['hello'])
    // The banner is unchanged by this fix.
    expect(result.current.error).toBeTruthy()
    // The refusal is scoped to the send: it must not stop the session.
    expect(result.current.isProcessing).toBe(false)
  })

  test('retrying a refused bubble re-sends it and clears the marker', async () => {
    const { result, waitUntilReady, sent } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      await result.current.onSend('hello', [])
    })
    await act(async () => {
      result.current.handleEvent({ event: 'error', data: RATE_LIMITED })
    })
    const failedBubble = result.current.messages.find((m) => m.role === 'user')
    expect(failedBubble?.failed).toBe(true)

    const sendsBefore = sent.filter((e) => e.event === 'message').length
    await act(async () => {
      result.current.retryMessage(failedBubble as ChatMessage)
    })

    // The retry path removes the failed bubble and sends the same payload again
    // (`retryMessage` -> `sendMessage`), so exactly one fresh bubble is left.
    const userTurns = () => result.current.messages.filter((m) => m.role === 'user')
    expect(userTurns().length).toBe(1)
    expect(userTurns()[0].failed).toBeUndefined()
    expect(sent.filter((e) => e.event === 'message').length).toBe(sendsBefore + 1)
  })
})

// ── Dispatcher level: which bubble, and which session ───────────────────────

type TestCtx = {
  ctx: MessageEventContext
  /** Streaming state as the dispatcher left it (`streamingMessagesRef` applied). */
  streaming: () => ChatMessage[]
  /** How many times the handler actually published an update. */
  updates: () => number
}

/**
 * A `MessageEventContext` stub whose `setStreamingMessages` really applies the
 * update to the ref, the way React state does once it settles. This is what
 * makes the "which bubble was marked" assertions meaningful rather than a
 * record of the updater's arguments.
 */
function makeDispatcherCtx(current: ChatMessage[], sessionKey: string | null): TestCtx {
  const ref = { current: [...current] }
  let updates = 0
  const ctx = {
    currentSessionKeyRef: { current: sessionKey },
    parentSessionKeyRef: { current: null },
    streamingMessagesRef: ref,
    setStreamingMessages: (next: ChatMessage[] | ((prev: ChatMessage[]) => ChatMessage[])) => {
      updates += 1
      ref.current = typeof next === 'function' ? next(ref.current) : next
    },
    setPendingAttachments: () => {},
    // The banner is covered by serverError.test.ts; keep it out of the way.
    setError: () => {},
  } as unknown as MessageEventContext
  return { ctx, streaming: () => ref.current, updates: () => updates }
}

function userMsg(
  id: string,
  content: string,
  sessionKey: string,
  extra: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role: 'user',
    content,
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey,
    optimistic: true,
    ...extra,
  }
}

describe('handleServerError — only the NEWEST refused bubble is marked', () => {
  test('a refused send leaves an EARLIER optimistic bubble of the session alone', () => {
    const { ctx, streaming } = makeDispatcherCtx(
      [
        userMsg('u1', 'first send', SESSION_KEY),
        userMsg('u2', 'second send', SESSION_KEY, { streaming: true }),
      ],
      SESSION_KEY,
    )

    dispatchMessageEvent(ctx, { event: 'error', data: RATE_LIMITED })

    const [first, second] = streaming()
    // The refusal answers exactly one send, and the envelope carries no client
    // message id (`sendError` in pkg/channels/websocket.go): the newest one.
    expect(second.failed).toBe(true)
    expect(second.streaming).toBe(false)
    // The earlier send was accepted; labelling it failed would be a lie (that
    // blanket rule is what `markOptimisticUserFailed` is for, per-turn only).
    expect(first.failed).toBeUndefined()
    expect(first.streaming).toBe(false)
  })

  test('with two rapid sends the NEWEST is the one marked', () => {
    const { ctx, streaming } = makeDispatcherCtx(
      [
        userMsg('u1', 'one', SESSION_KEY),
        { ...userMsg('a0', '', SESSION_KEY), role: 'assistant', optimistic: false },
        userMsg('u2', 'two', SESSION_KEY),
      ],
      SESSION_KEY,
    )

    dispatchMessageEvent(ctx, { event: 'error', data: RATE_LIMITED })

    const byId = (id: string) => streaming().find((m) => m.id === id)
    expect(byId('u2')?.failed).toBe(true)
    expect(byId('u1')?.failed).toBeUndefined()
  })

  test('a refusal that belongs to another session marks nothing (no state update)', () => {
    const { ctx, streaming, updates } = makeDispatcherCtx(
      [userMsg('u1', 'mine', SESSION_KEY)],
      // The UI is on another conversation's key, so its bubbles are untouched.
      'native:another-session',
    )

    dispatchMessageEvent(ctx, { event: 'error', data: RATE_LIMITED })

    expect(streaming()[0].failed).toBeUndefined()
    // Nothing changed → the handler must NOT schedule a state update at all.
    expect(updates()).toBe(0)
  })

  test('a late duplicate of the same refusal does not update again', () => {
    const { ctx, streaming, updates } = makeDispatcherCtx(
      [userMsg('u1', 'hello', SESSION_KEY)],
      SESSION_KEY,
    )
    const refuse = () => dispatchMessageEvent(ctx, { event: 'error', data: RATE_LIMITED })

    refuse()
    expect(updates()).toBe(1)
    expect(streaming()[0].failed).toBe(true)

    // Idempotent: the newest bubble is already failed, so the helper returns
    // the same array and the identity check skips the publish.
    refuse()
    expect(updates()).toBe(1)
    expect(streaming()[0].failed).toBe(true)
  })

  test('attachments_rejected still RETRACTS instead of marking the bubble failed', () => {
    const staging = '/lele/tmp/uploads/ab12cd34_photo.jpg'
    const { ctx, streaming } = makeDispatcherCtx(
      [
        userMsg('u1', '', SESSION_KEY, {
          attachments: [{ path: staging, name: 'photo.jpg', kind: 'image' }],
        }),
      ],
      SESSION_KEY,
    )

    dispatchMessageEvent(ctx, {
      event: 'error',
      data: {
        code: 'attachments_rejected',
        message: 'attachments could not be read; add text or re-upload',
      },
    })

    // The strictly better outcome: the turn is handed back to the composer
    // (MENOR-1), so there is nothing left on screen to mark as failed.
    expect(streaming()).toEqual([])
  })
})
