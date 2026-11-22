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
    const { result } = renderAppLogic({ token: null })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })

    let paths: string[] | undefined
    await act(async () => {
      paths = await result.current.onUploadAttachments([new File(['x'], 'photo.png')])
    })

    expect(paths).toEqual([])
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
    expect(result.current.isProcessing).toBe(false)
    expect(sessionKey.length).toBeGreaterThan(0)
  })
})

const SESSION_KEY = 'native:attach-refused'
const STAGING = '/lele/tmp/uploads/ab12cd34_photo.jpg'

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

    expect(result.current.error).toBe(i18n.t('chat.errorAttachmentsRejected') as string)
    expect(userTurns()).toEqual([])
    expect(result.current.pendingAttachments).toEqual([STAGING])
  })

  test('MENOR-1: no matching bubble is the banner alone — nothing retracted, no crash', async () => {
    const { result, waitUntilReady } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

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
    const userTurns = () => result.current.messages.filter((m) => m.role === 'user')
    expect(userTurns().map((m) => m.content)).toEqual(['hello'])
    expect(result.current.pendingAttachments).toEqual([])

    await refuse()
    expect(userTurns().map((m) => m.content)).toEqual(['hello'])
    expect(result.current.pendingAttachments).toEqual([])
  })
})

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
    expect(userTurns().length).toBe(1)
    expect(userTurns()[0].optimistic).toBe(true)
    expect(userTurns()[0].failed).toBeUndefined()

    await act(async () => {
      result.current.handleEvent({ event: 'error', data: RATE_LIMITED })
    })

    expect(userTurns()[0].failed).toBe(true)
    expect(userTurns().map((m) => m.content)).toEqual(['hello'])
    expect(result.current.error).toBeTruthy()
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

    const userTurns = () => result.current.messages.filter((m) => m.role === 'user')
    expect(userTurns().length).toBe(1)
    expect(userTurns()[0].failed).toBeUndefined()
    expect(sent.filter((e) => e.event === 'message').length).toBe(sendsBefore + 1)
  })
})

type TestCtx = {
  ctx: MessageEventContext
  streaming: () => ChatMessage[]
  updates: () => number
}

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
    expect(second.failed).toBe(true)
    expect(second.streaming).toBe(false)
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
      'native:another-session',
    )

    dispatchMessageEvent(ctx, { event: 'error', data: RATE_LIMITED })

    expect(streaming()[0].failed).toBeUndefined()
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

    expect(streaming()).toEqual([])
  })
})
