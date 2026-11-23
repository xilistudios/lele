import { describe, expect, test } from 'bun:test'
import type { Attachment, ChatMessage } from '../lib/types'
import { assistantMessageIdentity } from '../lib/messageIdentity'
import { mergeMessages } from './messageMerge'

const SESSION_KEY = 's1'
const NOW = '2026-11-23T00:00:00.000Z'

const image = (path: string, name = 'photo.png'): Attachment => ({
  path,
  name,
  mime_type: 'image/png',
  kind: 'image',
})

function makeMessage(
  id: string,
  role: ChatMessage['role'],
  content: string,
  options: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role,
    content,
    streaming: false,
    createdAt: NOW,
    sessionKey: SESSION_KEY,
    ...options,
  }
}

const liveDisplayBubble = (id: string, attachments: Attachment[]) =>
  makeMessage(id, 'assistant', '', { attachments })

const persistedRow = (id: string, attachments: Attachment[]) =>
  makeMessage(id, 'assistant', '', { attachments })

describe('assistantMessageIdentity', () => {
  test('prefers the text when there is one', () => {
    expect(assistantMessageIdentity('hola', undefined, [image('/a.png')])).toBe('hola')
  })

  test('falls back to the sorted attachment paths for caption-less rows', () => {
    expect(assistantMessageIdentity('', undefined, [image('/b.png'), image('/a.png')])).toBe(
      '/a.png\u0000/b.png',
    )
  })

  test('is empty when there is neither text nor attachments', () => {
    expect(assistantMessageIdentity('', undefined, [])).toBe('')
  })
})

describe('mergeMessages — persisted send_file display rows', () => {
  const PATH = '/data/lele/attachments/20261123/aa11_photo.png'

  test('live attachments-only bubble + persisted row with the same path collapse into ONE row', () => {
    const live = liveDisplayBubble('ack-file-1', [image(PATH)])
    const base = [persistedRow('hash-file-1', [image(PATH)])]

    const merged = mergeMessages(base, [live])

    expect(merged).toHaveLength(1)
    expect(merged[0].attachments?.map((a) => a.path)).toEqual([PATH])
    expect(merged[0].id).toBe('hash-file-1')
    expect(merged[0].stableId).toBe('ack-file-1')
  })

  test('two dispatches with different paths stay as TWO rows', () => {
    const live = liveDisplayBubble('ack-file-2', [image('/att/second.png')])
    const base = [persistedRow('hash-file-1', [image('/att/first.png')])]

    const merged = mergeMessages(base, [live])

    expect(merged).toHaveLength(2)
    expect(merged.map((m) => m.attachments?.[0].path)).toEqual([
      '/att/first.png',
      '/att/second.png',
    ])
  })

  test('a persisted row never consumes a live bubble whose attachments differ', () => {
    const live = liveDisplayBubble('ack-file-3', [image('/att/other.png')])
    const base = [persistedRow('hash-file-1', [image('/att/first.png')])]

    const merged = mergeMessages(base, [live])

    expect(merged.filter((m) => m.role === 'assistant')).toHaveLength(2)
  })

  test('a single-image live bubble is not consumed by a two-image persisted row (no prefix match)', () => {
    const live = liveDisplayBubble('ack-file-4', [image('/att/1.png')])
    const base = [persistedRow('hash-file-1', [image('/att/1.png'), image('/att/2.png')])]

    const merged = mergeMessages(base, [live])

    expect(merged).toHaveLength(2)
    expect(merged[1].attachments?.map((a) => a.path)).toEqual(['/att/1.png'])
  })

  test('the live bubble survives while the cache still lacks the row (no flicker)', () => {
    const live = liveDisplayBubble('ack-file-5', [image('/att/new.png')])

    const merged = mergeMessages([], [live])

    expect(merged).toHaveLength(1)
    expect(merged[0].id).toBe('ack-file-5')
  })

  test('a text-only live bubble still merges with its persisted row (no regression)', () => {
    const live = makeMessage('ack-text', 'assistant', 'here is your chart')
    const base = [makeMessage('hash-text', 'assistant', 'here is your chart')]

    const merged = mergeMessages(base, [live])

    expect(merged).toHaveLength(1)
    expect(merged[0].id).toBe('hash-text')
  })
})