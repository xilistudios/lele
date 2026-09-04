import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'
import { computeAssistantInsertIndex, computeToolInsertIndex } from './messageInsertion'
import {
  applyMessageComplete,
  attachToLastAssistant,
  markOptimisticUserFailed,
  markStreamingAssistantsErrored,
  migrateRestoreId,
  removeRestorePlaceholders,
  restoreInProgressAssistant,
  stopAllStreaming,
  upsertAssistantPlaceholder,
} from './streamingOps'

function msg(
  id: string,
  role: ChatMessage['role'],
  content: string,
  extra: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role,
    content,
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey: 's1',
    ...extra,
  }
}

// ── messageInsertion ────────────────────────────────────────────────────────

describe('computeToolInsertIndex', () => {
  test('appends at end when no assistant exists', () => {
    const current = [msg('t1', 'tool', '', { toolName: 'exec' })]
    expect(computeToolInsertIndex(current)).toBe(1)
  })

  test('inserts after assistant and its trailing tools (chronological)', () => {
    const current = [
      msg('u1', 'user', 'hi'),
      msg('a1', 'assistant', 'working'),
      msg('t1', 'tool', '', { toolName: 'exec' }),
      msg('t2', 'tool', '', { toolName: 'read_file' }),
    ]
    // After t2 → index 4
    expect(computeToolInsertIndex(current)).toBe(4)
  })

  test('inserts right after assistant when no tools trail it', () => {
    const current = [msg('u1', 'user', 'hi'), msg('a1', 'assistant', 'working')]
    expect(computeToolInsertIndex(current)).toBe(2)
  })
})

describe('computeAssistantInsertIndex (re-exported from messageInsertion)', () => {
  test('inserts after a user that follows the last assistant', () => {
    const current = [
      msg('a1', 'assistant', 'first', { streaming: false }),
      msg('u2', 'user', 'second', { optimistic: true }),
    ]
    expect(computeAssistantInsertIndex(current)).toBe(2)
  })
})

// ── streamingOps ────────────────────────────────────────────────────────────

describe('migrateRestoreId', () => {
  test('renames restore placeholder to real id', () => {
    const current = [msg('restore-s1', 'assistant', 'partial', { streaming: true })]
    const next = migrateRestoreId(current, 'real-uuid', 's1')
    expect(next[0].id).toBe('real-uuid')
    expect(next[0].content).toBe('partial')
  })

  test('no-op when real id already present', () => {
    const current = [
      msg('restore-s1', 'assistant', 'partial', { streaming: true }),
      msg('real-uuid', 'assistant', 'real', { streaming: true }),
    ]
    const next = migrateRestoreId(current, 'real-uuid', 's1')
    expect(next).toBe(current)
  })

  test('no-op when no restore placeholder for session', () => {
    const current = [msg('a1', 'assistant', 'x')]
    expect(migrateRestoreId(current, 'real', 's1')).toBe(current)
  })
})

describe('removeRestorePlaceholders', () => {
  test('drops only restore placeholders for the session', () => {
    const current = [
      msg('restore-s1', 'assistant', 'x'),
      msg('restore-s2', 'assistant', 'y', { sessionKey: 's2' }),
      msg('a1', 'assistant', 'z'),
    ]
    const next = removeRestorePlaceholders(current, 's1')
    expect(next.map((m) => m.id)).toEqual(['restore-s2', 'a1'])
  })
})

describe('upsertAssistantPlaceholder', () => {
  test('returns unchanged when message exists', () => {
    const current = [msg('a1', 'assistant', 'hi', { streaming: true })]
    expect(upsertAssistantPlaceholder(current, 'a1', 's1')).toBe(current)
  })

  test('inserts new placeholder at chronological position', () => {
    const current = [
      msg('a1', 'assistant', 'first', { streaming: false }),
      msg('u2', 'user', 'second', { optimistic: true }),
    ]
    const next = upsertAssistantPlaceholder(current, 'a2', 's1')
    expect(next.map((m) => m.id)).toEqual(['a1', 'u2', 'a2'])
  })
})

describe('restoreInProgressAssistant', () => {
  test('merges into existing restore placeholder', () => {
    const current = [msg('restore-s1', 'assistant', 'old', { streaming: true })]
    const next = restoreInProgressAssistant(current, 's1', 'new', '', false)
    expect(next[0].content).toBe('new')
    expect(next.length).toBe(1)
  })

  test('inserts after last user for session when flag set', () => {
    const current = [msg('u1', 'user', 'q'), msg('a-old', 'assistant', 'prev')]
    const next = restoreInProgressAssistant(current, 's1', 'restored', '', true)
    // inserted right after u1 (index 1), before a-old
    expect(next.map((m) => m.id)).toEqual(['u1', 'restore-s1', 'a-old'])
  })

  test('appends at end when flag not set', () => {
    const current = [msg('u1', 'user', 'q')]
    const next = restoreInProgressAssistant(current, 's1', 'restored', '', false)
    expect(next.map((m) => m.id)).toEqual(['u1', 'restore-s1'])
  })
})

describe('applyMessageComplete', () => {
  test('marks assistant complete, drops restore + empty user, stops tools', () => {
    const current = [
      msg('restore-s1', 'assistant', 'stale'),
      msg('u1', 'user', 'hi'),
      msg('empty-u', 'user', '   '),
      msg('a1', 'assistant', 'streamed', { streaming: true }),
      msg('t1', 'tool', '', { toolName: 'exec', streaming: true }),
    ]
    const next = applyMessageComplete(current, 'a1', 's1', undefined)
    expect(next.map((m) => m.id)).toEqual(['u1', 'a1', 't1'])
    expect(next.find((m) => m.id === 'a1')?.streaming).toBe(false)
    expect(next.find((m) => m.id === 'a1')?.content).toBe('streamed') // kept (server empty)
    expect(next.find((m) => m.id === 't1')?.streaming).toBe(false)
  })

  test('overwrites content when server sends non-empty final version', () => {
    const current = [msg('a1', 'assistant', 'partial', { streaming: true })]
    const next = applyMessageComplete(current, 'a1', 's1', 'FINAL')
    expect(next[0].content).toBe('FINAL')
  })

  test('creates the message when complete arrives before any stream chunk drained', () => {
    // message.complete can arrive before the typewriter queue's first tick
    // (or when stream events are coalesced). The message must still appear
    // with the final content instead of being silently dropped.
    const current = [msg('u1', 'user', 'hi')]
    const next = applyMessageComplete(current, 'a1', 's1', 'Parent response')
    expect(next.map((m) => m.id)).toEqual(['u1', 'a1'])
    expect(next.find((m) => m.id === 'a1')?.content).toBe('Parent response')
    expect(next.find((m) => m.id === 'a1')?.streaming).toBe(false)
  })

  test('creates empty message when complete has no content and nothing was streamed', () => {
    const current: ChatMessage[] = []
    const next = applyMessageComplete(current, 'a1', 's1', undefined)
    expect(next).toHaveLength(1)
    expect(next[0].id).toBe('a1')
    expect(next[0].content).toBe('')
    expect(next[0].streaming).toBe(false)
  })

  test('attaches server attachments to the completed message (replacing existing)', () => {
    const stale = [{ path: '/stale.png', name: 'stale.png', kind: 'image' as const }]
    const server = [
      {
        name: 'report.pdf',
        path: '/home/u/.lele/tmp/attachments/ab12_report.pdf',
        mime_type: 'application/pdf',
        kind: 'file',
        caption: 'monthly report',
      },
    ]
    const current = [msg('a1', 'assistant', 'done', { streaming: true, attachments: stale })]
    const next = applyMessageComplete(current, 'a1', 's1', 'done', server)
    expect(next[0].attachments).toEqual(server)
    expect(next[0].streaming).toBe(false)
  })

  test('preserves existing attachments when server sends none', () => {
    const prev = [{ path: '/f.png', name: 'f.png', kind: 'image' as const }]
    const current = [msg('a1', 'assistant', 'done', { streaming: true, attachments: prev })]
    const nextUndefined = applyMessageComplete(current, 'a1', 's1', 'done', undefined)
    expect(nextUndefined[0].attachments).toEqual(prev)
    const nextEmpty = applyMessageComplete(current, 'a1', 's1', 'done', [])
    expect(nextEmpty[0].attachments).toEqual(prev)
  })

  test('creates never-seen message including server attachments', () => {
    const server = [{ name: 'a.txt', path: '/tmp/a.txt', kind: 'file' as const }]
    const current = [msg('u1', 'user', 'hi')]
    const next = applyMessageComplete(current, 'a1', 's1', 'Here is the file', server)
    expect(next.map((m) => m.id)).toEqual(['u1', 'a1'])
    expect(next.find((m) => m.id === 'a1')?.attachments).toEqual(server)
    expect(next.find((m) => m.id === 'a1')?.streaming).toBe(false)
  })

  test('never-seen message without attachments has none', () => {
    const current = [msg('u1', 'user', 'hi')]
    const next = applyMessageComplete(current, 'a1', 's1', 'plain', undefined)
    expect(next.find((m) => m.id === 'a1')?.attachments).toBeUndefined()
  })
})

describe('markOptimisticUserFailed', () => {
  test('marks only optimistic users for the session', () => {
    const current = [
      msg('u1', 'user', 'hi', { optimistic: true }),
      msg('u2', 'user', 'real'),
      msg('u3', 'user', 'other', { optimistic: true, sessionKey: 's2' }),
    ]
    const next = markOptimisticUserFailed(current, 's1')
    expect(next[0].failed).toBe(true)
    expect(next[1].failed).toBeUndefined()
    expect(next[2].failed).toBeUndefined()
  })
})

describe('markStreamingAssistantsErrored', () => {
  test('marks streaming assistants for session as errored', () => {
    const current = [
      msg('a1', 'assistant', 'x', { streaming: true }),
      msg('a2', 'assistant', 'y', { streaming: false }),
      msg('a3', 'assistant', 'z', { streaming: true, sessionKey: 's2' }),
    ]
    const next = markStreamingAssistantsErrored(current, 's1', 'boom')
    expect(next[0].error).toBe('boom')
    expect(next[0].streaming).toBe(false)
    expect(next[1].error).toBeUndefined()
    expect(next[2].error).toBeUndefined()
  })
})

describe('stopAllStreaming', () => {
  test('clears streaming flag on every message', () => {
    const current = [
      msg('a1', 'assistant', 'x', { streaming: true }),
      msg('t1', 'tool', '', { streaming: true }),
    ]
    const next = stopAllStreaming(current)
    expect(next.every((m) => m.streaming === false)).toBe(true)
  })
})

describe('attachToLastAssistant', () => {
  test('attaches files to most recent assistant and stops streaming', () => {
    const current = [
      msg('a1', 'assistant', 'first', { streaming: false }),
      msg('a2', 'assistant', 'second', { streaming: true }),
    ]
    const attachments = [{ path: '/f.png', name: 'f.png', kind: 'image' as const }]
    const next = attachToLastAssistant(current, attachments)
    expect(next[1].attachments).toEqual(attachments)
    expect(next[1].streaming).toBe(false)
    expect(next[0].attachments).toBeUndefined()
  })

  test('no-op when no assistant exists', () => {
    const current = [msg('u1', 'user', 'hi')]
    expect(attachToLastAssistant(current, [])).toBe(current)
  })
})
