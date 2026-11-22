import '../test/setup'
import '../test/i18n'

import { afterEach, describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { Attachment, ChatMessage } from '../lib/types'
import { renderAppLogic } from '../test/appLogicHarness'
import {
  ATTACHMENTS_ONLY_MARKER,
  isAttachmentsOnly,
  isAttachmentsOnlyBase,
  mergeMessages,
} from './messageMerge'
import { useMessages } from './useMessages'

const SESSION_KEY = 'native:attach-test'
const NOW = '2026-10-17T00:00:00.000Z'

let autoSeq = 0
const autoId = (prefix: string) => `${prefix}${++autoSeq}`

const STAGING = '/lele/tmp/uploads/ab12cd34_photo.jpg'
const PERSISTED = '/data/lele/attachments/20261017/ff99aa11_photo.jpg'

const attachment = (path: string, name = 'photo.jpg'): Attachment => ({
  path,
  name,
  kind: 'image',
})

function makeUser(id: string, content: string, opts: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id,
    role: 'user',
    content,
    streaming: false,
    createdAt: NOW,
    sessionKey: SESSION_KEY,
    ...opts,
  }
}

const optUser = (content: string, opts: Partial<ChatMessage> = {}) =>
  makeUser(opts.id ?? autoId('opt-u'), content, { ...opts, optimistic: true })

describe('attachments-only identity helpers', () => {
  test('an empty user with an attachment is attachments-only; without one it is not', () => {
    expect(isAttachmentsOnly(optUser('', { attachments: [attachment(STAGING)] }))).toBe(true)
    expect(isAttachmentsOnly(optUser('   ', { attachments: [attachment(STAGING)] }))).toBe(true)
    expect(isAttachmentsOnly(optUser('', {}))).toBe(false)
    expect(isAttachmentsOnly(optUser('look at this', { attachments: [attachment(STAGING)] }))).toBe(
      false,
    )
    expect(
      isAttachmentsOnly({
        ...makeUser('a1', '', { attachments: [attachment(STAGING)] }),
        role: 'assistant',
      }),
    ).toBe(false)
    expect(
      isAttachmentsOnly(
        optUser(`${ATTACHMENTS_ONLY_MARKER}\n- ${STAGING}`, {
          attachments: [attachment(STAGING)],
        }),
      ),
    ).toBe(false)
  })

  test('the persisted copy is recognised with the marker AND once parsed into attachments', () => {
    const raw = makeUser('h1', `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}`)
    expect(isAttachmentsOnlyBase(raw)).toBe(true)

    const [parsed] = toChatMessages(
      [{ id: 'h2', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}` }],
      SESSION_KEY,
    )
    expect(parsed.content).toBe('')
    expect(parsed.attachments?.length).toBe(1)
    expect(isAttachmentsOnlyBase(parsed)).toBe(true)

    expect(isAttachmentsOnlyBase(makeUser('h3', 'look at this'))).toBe(false)
    expect(isAttachmentsOnlyBase(optUser('', { attachments: [attachment(STAGING)] }))).toBe(false)
  })
})

describe('mergeMessages — attachments-only optimistic bubbles', () => {
  test('3: the bubble is retired when the persisted copy landed (parsed shape)', () => {
    const base = toChatMessages(
      [{ id: 'h1', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}` }],
      SESSION_KEY,
    )
    const optimistic = optUser('', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })

    const merged = mergeMessages(base, [optimistic])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.filter((m) => m.role === 'user').length).toBe(1)
    expect(merged[0].attachments?.[0].path).toBe(PERSISTED)
  })

  test('3b: same, for the raw history shape (content still carries the marker)', () => {
    const base = [makeUser('h1', `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}`)]
    const optimistic = optUser('', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })

    const merged = mergeMessages(base, [optimistic])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.filter((m) => m.role === 'user').length).toBe(1)
  })

  test('3c: while the history copy has NOT landed, the bubble stays (nothing to lose)', () => {
    const optimistic = optUser('', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })

    const merged = mergeMessages([], [optimistic])

    expect(merged.filter((m) => m.optimistic === true).length).toBe(1)
    expect(merged[0].attachments?.[0].path).toBe(STAGING)
  })

  test('4: two consecutive attachments-only sends are retired in order', () => {
    const base = toChatMessages(
      [
        { id: 'h1', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}` },
        { id: 'h2', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- /data/x/b.jpg` },
      ],
      SESSION_KEY,
    )
    const first = optUser('', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })
    const second = optUser('', {
      id: 'opt-2',
      optimisticBaseCount: 0,
      attachments: [attachment('/lele/tmp/uploads/bb_other.jpg')],
    })

    const merged = mergeMessages(base, [first, second])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.filter((m) => m.role === 'user').length).toBe(2)
  })

  test('4b: with only ONE copy in base, the second bubble survives (no claim stealing)', () => {
    const base = toChatMessages(
      [{ id: 'h1', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}` }],
      SESSION_KEY,
    )
    const first = optUser('', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })
    const second = optUser('', {
      id: 'opt-2',
      optimisticBaseCount: 0,
      attachments: [attachment('/lele/tmp/uploads/bb_other.jpg')],
    })

    const merged = mergeMessages(base, [first, second])

    const stillOptimistic = merged.filter((m) => m.optimistic === true)
    expect(stillOptimistic.map((m) => m.id)).toEqual(['opt-2'])
  })

  test('5: a text+attachments send is NOT confirmed by an attachments-only copy', () => {
    const base = toChatMessages(
      [{ id: 'h1', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}` }],
      SESSION_KEY,
    )
    const withText = optUser('look at this', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })

    const merged = mergeMessages(base, [withText])

    expect(merged.filter((m) => m.optimistic === true).map((m) => m.id)).toEqual(['opt-1'])
  })

  test('5c: a message whose TEXT is the marker block keeps its text identity', () => {
    const base = toChatMessages(
      [{ id: 'h1', role: 'user', content: `${ATTACHMENTS_ONLY_MARKER}\n- ${PERSISTED}` }],
      SESSION_KEY,
    )
    const typedMarker = optUser(`${ATTACHMENTS_ONLY_MARKER}\n- ${STAGING}`, {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })

    const merged = mergeMessages(base, [typedMarker])

    expect(merged.filter((m) => m.optimistic === true).map((m) => m.id)).toEqual(['opt-1'])
  })

  test('5b: an attachments-only bubble is NOT confirmed by a base user WITH text', () => {
    const base = toChatMessages([{ id: 'h1', role: 'user', content: 'look at this' }], SESSION_KEY)
    const optimistic = optUser('', {
      id: 'opt-1',
      optimisticBaseCount: 0,
      attachments: [attachment(STAGING)],
    })

    const merged = mergeMessages(base, [optimistic])

    expect(merged.filter((m) => m.optimistic === true).map((m) => m.id)).toEqual(['opt-1'])
  })
})

type SentEvent = { event: string; data: Record<string, unknown> }

function renderMessages() {
  const sent: SentEvent[] = []
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children)
  const rendered = renderHook(
    () =>
      useMessages(
        (event, data) => sent.push({ event, data }),
        SESSION_KEY,
        { current: SESSION_KEY },
        undefined,
        null,
      ),
    { wrapper },
  )
  return { ...rendered, sent }
}

const sentMessages = (sent: SentEvent[]) => sent.filter((e) => e.event === 'message')

describe('sendMessage — attachments-only turns', () => {
  test('1: text empty + an attachment is sent, and gets an optimistic bubble', async () => {
    const { result, sent } = renderMessages()

    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.sendMessage('', [STAGING], SESSION_KEY, 'main')
    })

    expect(accepted).toBe(true)
    const messages = sentMessages(sent)
    expect(messages.length).toBe(1)
    expect(messages[0].data.content).toBe('')
    expect(messages[0].data.attachments).toEqual([STAGING])
    expect(messages[0].data.session_key).toBe(SESSION_KEY)

    const bubbles = result.current.streamingMessages
    expect(bubbles.length).toBe(1)
    expect(bubbles[0].content).toBe('')
    expect(bubbles[0].attachments?.map((a) => a.path)).toEqual([STAGING])
    expect(bubbles[0].optimistic).toBe(true)
    expect(bubbles[0].optimisticBaseCount).toBe(0)
  })

  test('2: text empty and NO attachment is refused — nothing is sent, nothing is added', async () => {
    const { result, sent } = renderMessages()

    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.sendMessage('', [], SESSION_KEY, 'main')
    })

    expect(accepted).toBe(false)
    expect(sentMessages(sent).length).toBe(0)
    expect(result.current.streamingMessages.length).toBe(0)
  })

  test('3: whitespace-only text with an attachment trims to an empty send (not refused)', async () => {
    const { result, sent } = renderMessages()

    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.sendMessage('   ', [STAGING], SESSION_KEY, 'main')
    })

    expect(accepted).toBe(true)
    expect(sentMessages(sent)[0].data.content).toBe('')
    expect(result.current.streamingMessages[0].content).toBe('')
  })

  test('4: no session key is a refusal, not a silent drop', async () => {
    const { result, sent } = renderMessages()

    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.sendMessage('', [STAGING], '', 'main')
    })

    expect(accepted).toBe(false)
    expect(sentMessages(sent).length).toBe(0)
  })
})

describe('useAppLogic.onSend — attachments-only turns', () => {
  afterEach(() => cleanup())

  test('6: an accepted attachments-only send clears the chips', async () => {
    const { result, sent, waitUntilReady } = renderAppLogic({ sessionKey: SESSION_KEY })
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
    expect(sentMessages(sent)[0].data.attachments).toEqual([STAGING])
  })

  test('7: a REFUSED send keeps the chips (the user keeps their file)', async () => {
    const { result, sent, waitUntilReady } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      result.current.onAttachmentsChange([STAGING])
    })
    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.onSend('', [])
    })

    expect(accepted).toBe(false)
    expect(result.current.pendingAttachments).toEqual([STAGING])
    expect(sentMessages(sent).length).toBe(0)
  })

  test('8: a queued attachments-only send (busy agent) clears the chips too', async () => {
    const { result, sent, waitUntilReady, sessionKey } = renderAppLogic({ sessionKey: SESSION_KEY })
    await waitUntilReady()

    await act(async () => {
      result.current.onAttachmentsChange([STAGING])
    })

    await act(async () => {
      result.current.handleEvent({
        event: 'message.ack',
        data: { session_key: sessionKey, message_id: 'ack-busy' },
      })
    })
    expect(result.current.isProcessing).toBe(true)

    let accepted: boolean | undefined
    await act(async () => {
      accepted = await result.current.onSend('', [STAGING])
    })

    expect(accepted).toBe(true)
    expect(result.current.queueCount(sessionKey)).toBe(1)
    expect(result.current.queuedMessages[0].attachments).toEqual([STAGING])
    expect(result.current.pendingAttachments).toEqual([])
    expect(sentMessages(sent).length).toBe(0)
  })
})
