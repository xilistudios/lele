import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'
import { computeAssistantInsertIndex, computeToolInsertIndex } from './messageInsertion'
import {
  applyMessageComplete,
  attachToLastAssistant,
  markNewestOptimisticUserFailed,
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

describe('markNewestOptimisticUserFailed', () => {
  test('marks the NEWEST optimistic user of the session and nothing else', () => {
    const current = [
      msg('u1', 'user', 'first', { optimistic: true }),
      msg('u2', 'user', 'second', { optimistic: true, streaming: true }),
      msg('a1', 'assistant', 'answer'),
    ]
    const next = markNewestOptimisticUserFailed(current, 's1')

    expect(next[1].failed).toBe(true)
    // The flag that keeps the composer's spinner logic honest.
    expect(next[1].streaming).toBe(false)
    // The earlier turn was accepted and may still be answered: untouched.
    expect(next[0].failed).toBeUndefined()
    expect(next[0].streaming).toBe(false)
    // Every other message is the very same object (nothing else re-renders).
    expect(next[2]).toBe(current[2])
    expect(next[0]).toBe(current[0])
    // Input is not mutated.
    expect(current[1].failed).toBeUndefined()
  })

  test('finds the newest optimistic user even when it is not the last message', () => {
    const current = [
      msg('u1', 'user', 'hi', { optimistic: true }),
      msg('u2', 'user', 'later send', { optimistic: true }),
      msg('u3', 'user', 'persisted'),
      msg('a1', 'assistant', 'answer'),
      msg('t1', 'tool', '', { toolName: 'exec' }),
    ]
    const next = markNewestOptimisticUserFailed(current, 's1')

    expect(next[1].failed).toBe(true)
    expect(next[0].failed).toBeUndefined()
    expect(next[2].failed).toBeUndefined()
  })

  test('returns the SAME array when the session has no optimistic user', () => {
    const current = [
      msg('u1', 'user', 'persisted'),
      msg('a1', 'assistant', 'answer', { streaming: true }),
    ]
    expect(markNewestOptimisticUserFailed(current, 's1')).toBe(current)
    // Empty list too (a refusal before any send was accepted locally).
    const empty: ChatMessage[] = []
    expect(markNewestOptimisticUserFailed(empty, 's1')).toBe(empty)
  })

  test('ignores optimistic users of another session', () => {
    const current = [
      msg('u1', 'user', 'mine', { optimistic: true, sessionKey: 's1' }),
      msg('u2', 'user', 'theirs', { optimistic: true, sessionKey: 's2' }),
    ]
    const next = markNewestOptimisticUserFailed(current, 's1')

    expect(next[0].failed).toBe(true)
    // The NEWEST optimistic message overall belongs to s2: it must stay clean.
    expect(next[1].failed).toBeUndefined()

    // A session with no bubble at all is a no-op (same instance).
    const foreignOnly = [msg('u2', 'user', 'theirs', { optimistic: true, sessionKey: 's2' })]
    expect(markNewestOptimisticUserFailed(foreignOnly, 's1')).toBe(foreignOnly)
  })

  test('is idempotent: an already-failed newest bubble is the SAME array', () => {
    const current = [
      msg('u1', 'user', 'first', { optimistic: true }),
      msg('u2', 'user', 'second', { optimistic: true, failed: true }),
    ]
    // A late/duplicate refusal must not schedule a pointless state update.
    expect(markNewestOptimisticUserFailed(current, 's1')).toBe(current)
  })

  test('matches a bubble carrying the conversation alias against the bare key', () => {
    // The WS handlers re-tag transient messages with the pane's effective
    // session key, which can be the aliased `base:chat:N` built by
    // pkg/agent/loop.go `nextConversationSessionKey` while the refusal handler
    // queries with the bare pane key (or vice versa). A strict `===` left the
    // bubble unmarked — stranded with no retry affordance — which is exactly
    // what this helper exists to prevent.
    //
    // NOTE the alias shape: the suffix is `:chat:<digits>` appended to the
    // WHOLE base key (`native:client-1` -> `native:client-1:chat:7`), so the
    // pair is base vs base+suffix. A bare `chat:7` has no `:chat:` prefix and
    // is therefore a root of its own (see the `chat:8` case below).
    const aliased = [
      msg('u1', 'user', 'aliased', {
        optimistic: true,
        streaming: true,
        sessionKey: 'native:client-1:chat:7',
      }),
    ]
    const next = markNewestOptimisticUserFailed(aliased, 'native:client-1')

    expect(next[0].failed).toBe(true)
    expect(next[0].streaming).toBe(false)
    // Input is not mutated (the flagged copy is a new object).
    expect(aliased[0].failed).toBeUndefined()

    // …and the mirror case: bubble on the bare key, queried with the alias.
    const bare = [
      msg('u1', 'user', 'bare', {
        optimistic: true,
        streaming: true,
        sessionKey: 'native:client-1',
      }),
    ]
    const mirror = markNewestOptimisticUserFailed(bare, 'native:client-1:chat:7')

    expect(mirror[0].failed).toBe(true)
    expect(mirror[0].streaming).toBe(false)
  })

  test('still ignores a bubble of a genuinely different session (chat:8)', () => {
    // Loosening the match must not widen it to another conversation. `chat:8`
    // has no `:chat:<digits>` suffix, so its root is itself and it can never
    // match `native:client-1` (or its alias). Same for another client's pane.
    //
    // NOTE (intentional, documented in event-handlers/helpers.ts): two ALIASES
    // of the SAME base (`native:client-1:chat:7` vs `…:chat:8`) DO loosely
    // match — one client, one visible conversation per base. That is not what
    // this test guards; it guards against matching ACROSS roots.
    const current = [
      msg('u1', 'user', 'mine', {
        optimistic: true,
        streaming: true,
        sessionKey: 'native:client-1',
      }),
      msg('u2', 'user', 'theirs', { optimistic: true, streaming: true, sessionKey: 'chat:8' }),
      msg('u3', 'user', 'other client', {
        optimistic: true,
        streaming: true,
        sessionKey: 'native:client-2:chat:7',
      }),
    ]
    const next = markNewestOptimisticUserFailed(current, 'native:client-1:chat:7')

    expect(next[0].failed).toBe(true)
    expect(next[1].failed).toBeUndefined()
    expect(next[1].streaming).toBe(true)
    expect(next[2].failed).toBeUndefined()
    expect(next[2].streaming).toBe(true)

    // A refusal for a session that has no bubble in this list is a no-op
    // (same instance, no pointless state update).
    const untouched = markNewestOptimisticUserFailed(current, 'native:client-9')
    expect(untouched).toBe(current)
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
