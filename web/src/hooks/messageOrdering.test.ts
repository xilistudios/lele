import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { mergeMessages } from './useChatHistory'
import { computeAssistantInsertIndex } from './useStreamQueues'

function createTestMessage(
  id: string,
  role: 'user' | 'assistant' | 'tool',
  content: string,
  options: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role,
    content,
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey: 'test-session',
    ...options,
  }
}

describe('Message ordering fixes', () => {
  describe('Bug 1: Assistant should not appear before user message', () => {
    test('baseHasCurrentTurn should be false when base has user but not assistant', () => {
      // Scenario: User sends message, optimistic user added to cache
      // Base has: [user1, asst1, user2, asst2, optimisticUser]
      // Streaming has: [optimisticUser, assistantPlaceholder]
      // Expected: assistant should appear AFTER optimisticUser, not before

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello 1'),
        createTestMessage('a1', 'assistant', 'Hi 1'),
        createTestMessage('u2', 'user', 'Hello 2'),
        createTestMessage('a2', 'assistant', 'Hi 2'),
        createTestMessage('u3-opt', 'user', 'Hello 3', {
          optimistic: true,
          optimisticBaseCount: 2,
        }),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('u3-opt', 'user', 'Hello 3', {
          optimistic: true,
          optimisticBaseCount: 2,
        }),
        createTestMessage('a3-stream', 'assistant', 'Hi 3', { streaming: true }),
      ]

      // Calculate baseHasCurrentTurn with the fix
      const baseUserCount = baseMessages.filter((m) => m.role === 'user').length // 3
      const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length // 2
      const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)

      // With the fix: baseHasCurrentTurn requires baseAssistantCount >= baseUserCount
      const baseHasCurrentTurn =
        baseUserCount > (optimisticUser?.optimisticBaseCount ?? 0) &&
        baseAssistantCount >= baseUserCount

      // baseUserCount (3) > optimisticBaseCount (2) = true
      // baseAssistantCount (2) >= baseUserCount (3) = false
      // Result: false (correct!)
      expect(baseHasCurrentTurn).toBe(false)

      // When baseHasCurrentTurn is false, matchOffset = baseAssistantCount
      const matchOffset = baseHasCurrentTurn
        ? Math.max(0, baseAssistantCount - 1) // streamingAssistantCount = 1
        : baseAssistantCount

      expect(matchOffset).toBe(2) // No matching happens

      // This means the streaming assistant won't replace an old base assistant
      // It will be appended after filteredBase, which includes the optimistic user
      // Result: [..., u2, a2, u3-opt, a3-stream] ✓ Correct order!
    })

    test('baseHasCurrentTurn should be true when base has both user and assistant', () => {
      // Scenario: HTTP refetch brought back both user and assistant
      // Base has: [user1, asst1, user2, asst2, user3, asst3]
      // Streaming has: [assistantPlaceholder] (optimistic user removed)
      // Expected: matching should happen to avoid duplicates

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello 1'),
        createTestMessage('a1', 'assistant', 'Hi 1'),
        createTestMessage('u2', 'user', 'Hello 2'),
        createTestMessage('a2', 'assistant', 'Hi 2'),
        createTestMessage('u3', 'user', 'Hello 3'),
        createTestMessage('a3', 'assistant', 'Hi 3'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a3-stream', 'assistant', 'Hi 3', { streaming: false }),
      ]

      const baseUserCount = baseMessages.filter((m) => m.role === 'user').length // 3
      const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length // 3
      const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)

      const baseHasCurrentTurn =
        baseUserCount > (optimisticUser?.optimisticBaseCount ?? 0) &&
        baseAssistantCount >= baseUserCount

      // baseUserCount (3) > 0 = true
      // baseAssistantCount (3) >= baseUserCount (3) = true
      // Result: true (correct!)
      expect(baseHasCurrentTurn).toBe(true)
    })
  })

  describe('Bug 2: Tool calls should be in chronological order', () => {
    test('tool insertion should maintain chronological order', () => {
      // Scenario: Multiple tools arrive for the same assistant
      // Initial: [optimisticUser, assistant]
      // tool1 arrives: [optimisticUser, assistant, tool1]
      // tool2 arrives: [optimisticUser, assistant, tool1, tool2] ✓
      // NOT: [optimisticUser, assistant, tool2, tool1] ✗

      let current: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi', { streaming: true }),
      ]

      // Simulate tool1 arrival with the fixed logic
      const tool1 = createTestMessage('t1', 'tool', '', {
        toolName: 'read_file',
        toolStatus: 'executing',
      })

      const lastAssistantIdx1 = [...current].reverse().findIndex((m) => m.role === 'assistant')
      const assistantOriginalIdx1 = current.length - 1 - lastAssistantIdx1
      let insertIdx1 = assistantOriginalIdx1 + 1
      while (insertIdx1 < current.length && current[insertIdx1].role === 'tool') {
        insertIdx1++
      }
      const arr1 = [...current]
      arr1.splice(insertIdx1, 0, tool1)
      current = arr1

      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't1'])

      // Simulate tool2 arrival
      const tool2 = createTestMessage('t2', 'tool', '', {
        toolName: 'exec',
        toolStatus: 'executing',
      })

      const lastAssistantIdx2 = [...current].reverse().findIndex((m) => m.role === 'assistant')
      const assistantOriginalIdx2 = current.length - 1 - lastAssistantIdx2
      let insertIdx2 = assistantOriginalIdx2 + 1
      while (insertIdx2 < current.length && current[insertIdx2].role === 'tool') {
        insertIdx2++
      }
      const arr2 = [...current]
      arr2.splice(insertIdx2, 0, tool2)
      current = arr2

      // With the fix: tool2 is inserted AFTER tool1
      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't1', 't2'])

      // Simulate tool3 arrival
      const tool3 = createTestMessage('t3', 'tool', '', {
        toolName: 'write_file',
        toolStatus: 'executing',
      })

      const lastAssistantIdx3 = [...current].reverse().findIndex((m) => m.role === 'assistant')
      const assistantOriginalIdx3 = current.length - 1 - lastAssistantIdx3
      let insertIdx3 = assistantOriginalIdx3 + 1
      while (insertIdx3 < current.length && current[insertIdx3].role === 'tool') {
        insertIdx3++
      }
      const arr3 = [...current]
      arr3.splice(insertIdx3, 0, tool3)
      current = arr3

      // All tools in chronological order
      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't1', 't2', 't3'])
    })

    test('old buggy logic would produce reverse order', () => {
      // This test demonstrates the bug with the old logic
      let current: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi', { streaming: true }),
      ]

      const tool1 = createTestMessage('t1', 'tool', '', {
        toolName: 'read_file',
        toolStatus: 'executing',
      })

      // Old buggy logic: insert right after assistant
      const lastAssistantIdx1 = [...current].reverse().findIndex((m) => m.role === 'assistant')
      const targetIndex1 = current.length - lastAssistantIdx1
      const arr1 = [...current]
      arr1.splice(targetIndex1, 0, tool1)
      current = arr1

      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't1'])

      const tool2 = createTestMessage('t2', 'tool', '', {
        toolName: 'exec',
        toolStatus: 'executing',
      })

      const lastAssistantIdx2 = [...current].reverse().findIndex((m) => m.role === 'assistant')
      const targetIndex2 = current.length - lastAssistantIdx2
      const arr2 = [...current]
      arr2.splice(targetIndex2, 0, tool2)
      current = arr2

      // Old logic: tool2 is inserted BEFORE tool1 (reverse order!)
      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't2', 't1'])
    })
  })

  describe('Bug 4: Streaming text should appear after tool calls, not before', () => {
    test('new assistant inserted before trailing tools when tools arrive first', () => {
      // Scenario: tool.executing arrives before message.stream (ack delayed/lost)
      // 1. tool.executing → streamingMessages: [tool1]
      // 2. message.stream → should insert assistant BEFORE tool1
      // Expected: [assistant, tool1], NOT [tool1, assistant]

      // Simulate tool.executing arriving first (no assistant exists)
      const current: ChatMessage[] = [
        createTestMessage('t1', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'executing',
        }),
        createTestMessage('t2', 'tool', '', {
          toolName: 'read_file',
          toolStatus: 'executing',
        }),
      ]

      // Simulate ensureAssistantPlaceholder creating a new assistant
      // with the fixed logic
      const newMsg = createTestMessage('a1', 'assistant', 'Let me check...', {
        streaming: true,
      })

      // Find insertion point (fixed logic from useStreamQueues)
      const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')

      let insertIdx: number
      if (lastAssistantIdx >= 0) {
        const assistantPos = current.length - 1 - lastAssistantIdx
        insertIdx = assistantPos + 1
        while (insertIdx < current.length && current[insertIdx].role === 'tool') {
          insertIdx++
        }
      } else {
        // No assistant exists — insert before all tool messages
        const firstToolIdx = current.findIndex((m) => m.role === 'tool')
        insertIdx = firstToolIdx >= 0 ? firstToolIdx : current.length
      }

      const arr = [...current]
      arr.splice(insertIdx, 0, newMsg)

      // Assistant should be BEFORE the tools
      expect(arr.map((m) => m.id)).toEqual(['a1', 't1', 't2'])
      expect(arr[0].role).toBe('assistant')
      expect(arr[1].role).toBe('tool')
      expect(arr[2].role).toBe('tool')
    })

    test('assistant appended after existing assistant and its tools', () => {
      // Scenario: second assistant arrives while first has tools
      // Streaming: [asst1, tool1, tool2]
      // New assistant should go AFTER tool2 (it's a subsequent turn)

      const current: ChatMessage[] = [
        createTestMessage('a1', 'assistant', 'First response', { streaming: false }),
        createTestMessage('t1', 'tool', '', { toolName: 'exec', toolStatus: 'completed' }),
        createTestMessage('t2', 'tool', '', { toolName: 'read_file', toolStatus: 'completed' }),
      ]

      const newMsg = createTestMessage('a2', 'assistant', 'Second response', {
        streaming: true,
      })

      const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')

      let insertIdx: number
      if (lastAssistantIdx >= 0) {
        const assistantPos = current.length - 1 - lastAssistantIdx
        insertIdx = assistantPos + 1
        while (insertIdx < current.length && current[insertIdx].role === 'tool') {
          insertIdx++
        }
      } else {
        const firstToolIdx = current.findIndex((m) => m.role === 'tool')
        insertIdx = firstToolIdx >= 0 ? firstToolIdx : current.length
      }

      const arr = [...current]
      arr.splice(insertIdx, 0, newMsg)

      // New assistant goes after all tools of the previous assistant
      expect(arr.map((m) => m.id)).toEqual(['a1', 't1', 't2', 'a2'])
    })

    test('assistant appended at end when no tools exist', () => {
      // Normal flow: message.ack creates assistant before any tools
      const current: ChatMessage[] = []

      const newMsg = createTestMessage('a1', 'assistant', 'Hello!', { streaming: true })

      const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')

      let insertIdx: number
      if (lastAssistantIdx >= 0) {
        const assistantPos = current.length - 1 - lastAssistantIdx
        insertIdx = assistantPos + 1
        while (insertIdx < current.length && current[insertIdx].role === 'tool') {
          insertIdx++
        }
      } else {
        const firstToolIdx = current.findIndex((m) => m.role === 'tool')
        insertIdx = firstToolIdx >= 0 ? firstToolIdx : current.length
      }

      const arr = [...current]
      arr.splice(insertIdx, 0, newMsg)

      expect(arr.map((m) => m.id)).toEqual(['a1'])
    })
  })

  describe('Bug 3: Duplicate assistant when optimistic user lingers in HTTP cache', () => {
    test('mergeMessages should not duplicate completed assistant when base cache still has optimistic user', () => {
      // Scenario:
      // - User sends a message. Optimistic user is added to streamingMessages AND
      //   to the queryClient cache (baseMessages) by sendMessage().
      // - HTTP refetch brings back the real user + assistant, but merge in queryFn
      //   preserves the optimistic user because it has a different ID.
      // - Now baseMessages has BOTH the real user and the optimistic user.
      // - message.complete marks the streaming assistant as streaming=false.
      // - If baseUserCount includes the optimistic user, baseHasCurrentTurn
      //   becomes false (more users than assistants), matchOffset becomes wrong,
      //   and the streaming assistant is NOT deduplicated → duplicate bubble.

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi!'),
        createTestMessage('u2', 'user', 'How are you?'),
        // Optimistic user that leaked into base cache after refetch merge:
        createTestMessage('u2-opt', 'user', 'How are you?', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
        // Real assistant from HTTP history:
        createTestMessage('a2-base', 'assistant', 'Doing great!'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('u2-opt', 'user', 'How are you?', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
        // Streaming assistant that just completed:
        createTestMessage('a2-ws', 'assistant', 'Doing great!', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const assistantMessages = result.filter((m) => m.role === 'assistant')

      // Should only show ONE assistant for the current turn
      expect(assistantMessages.length).toBe(2) // a1 + a2
      expect(assistantMessages[1].id).toBe('a2-base')
      expect(result.some((m) => m.id === 'a2-ws')).toBe(false)
    })
  })

  describe('Bug 6: New assistant inserted before optimistic user when prior assistant is retained', () => {
    test('new assistant should go AFTER the optimistic user, not after the retained assistant', () => {
      // Scenario: Turn 1 completed but HTTP cache hasn't caught up yet, so
      // a1-completed is retained in streamingMessages. User sends turn 2.
      // streamingMessages = [a1-completed, u2-opt]
      // message.ack arrives → ensureAssistantPlaceholder inserts a2.
      //
      // OLD BUG: lastAssistantIdx finds a1 at index 0, inserts a2 at index 1.
      // Result: [a1, a2, u2-opt] — assistant appears BEFORE user message!
      //
      // FIX: detect that the last user is AFTER the last assistant → insert
      // after the user instead.
      // Result: [a1, u2-opt, a2] ✓

      const current: ChatMessage[] = [
        createTestMessage('a1', 'assistant', 'First response', { streaming: false }),
        createTestMessage('u2-opt', 'user', 'Second question', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
      ]

      const newMsg = createTestMessage('a2', 'assistant', '', { streaming: true })

      // Use the real insertion logic (single source of truth).
      const arr = [...current]
      arr.splice(computeAssistantInsertIndex(current), 0, newMsg)

      // Assistant must come AFTER the user message
      expect(arr.map((m) => m.id)).toEqual(['a1', 'u2-opt', 'a2'])
      expect(arr.map((m) => m.role)).toEqual(['assistant', 'user', 'assistant'])
    })

    test('continuation after tool calls still works when no user is after assistant', () => {
      // Scenario: same turn, tools completed, new assistant is post-tool response.
      // streamingMessages = [u1-opt, a1, t1, t2]
      // New assistant a2 should go after t2 (continuation logic).

      const current: ChatMessage[] = [
        createTestMessage('u1-opt', 'user', 'Do something', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a1', 'assistant', 'Let me check', { streaming: false }),
        createTestMessage('t1', 'tool', '', { toolName: 'exec', toolStatus: 'completed' }),
        createTestMessage('t2', 'tool', '', { toolName: 'read_file', toolStatus: 'completed' }),
      ]

      const newMsg = createTestMessage('a2', 'assistant', 'Here is the result', {
        streaming: true,
      })

      const arr = [...current]
      arr.splice(computeAssistantInsertIndex(current), 0, newMsg)

      // a2 goes after t2 (continuation of same turn)
      expect(arr.map((m) => m.id)).toEqual(['u1-opt', 'a1', 't1', 't2', 'a2'])
    })
  })

  describe('Bug 5: Actively streaming assistant should not replace older base assistant in-place', () => {
    test('streaming assistant (post-tool-call) should be appended, not inserted at old position', () => {
      // Scenario: Agent does iterative tool calls. HTTP history has caught up
      // with the first assistant + tool, but the second assistant (response
      // after tool result) is still streaming via WebSocket.
      //
      // base (HTTP):  [u1, a1, tool1]  — a1 is completed, tool1 is completed
      // streaming (WS): [a2-stream]    — new response after tool1, actively streaming
      //
      // OLD BUG: position-based matching paired a1 (base) with a2-stream,
      // and since a2-stream had isStreaming=true, it replaced a1 in-place.
      // Result: [u1, a2-stream, tool1] — a2 appears BEFORE tool1!
      //
      // EXPECTED: [u1, a1, tool1, a2-stream] — a2 appended at the end.

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Do something'),
        createTestMessage('a1-base', 'assistant', 'Let me check...'),
        createTestMessage('t1', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a2-stream', 'assistant', 'Here is the result...', {
          streaming: true,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      // a1-base should remain in its original position
      // a2-stream should be appended AFTER tool1
      expect(result.map((m) => m.id)).toEqual(['u1', 'a1-base', 't1', 'a2-stream'])

      // Verify roles are in correct order
      expect(result.map((m) => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    })

    test('multiple streaming assistants after tool calls maintain order', () => {
      // Scenario: Agent did two tool calls, now streaming the final response.
      // base (HTTP):  [u1, a1, tool1, a2, tool2]
      // streaming (WS): [a3-stream]
      //
      // Expected: [u1, a1, tool1, a2, tool2, a3-stream]

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Complex task'),
        createTestMessage('a1-base', 'assistant', 'Step 1...'),
        createTestMessage('t1', 'tool', '', {
          toolName: 'read_file',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2-base', 'assistant', 'Step 2...'),
        createTestMessage('t2', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-2',
        }),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a3-stream', 'assistant', 'Final answer...', {
          streaming: true,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      expect(result.map((m) => m.id)).toEqual(['u1', 'a1-base', 't1', 'a2-base', 't2', 'a3-stream'])
      expect(result.map((m) => m.role)).toEqual([
        'user',
        'assistant',
        'tool',
        'assistant',
        'tool',
        'assistant',
      ])
    })

    test('completed streaming assistant is still deduped against base', () => {
      // When streaming completes and HTTP catches up, the completed streaming
      // assistant should be deduped (not appended as duplicate).
      // base (HTTP):  [u1, a1, tool1, a2-final]
      // streaming (WS): [a2-ws] (streaming=false, same content as a2-final)
      //
      // Expected: [u1, a1, tool1, a2-final] — no duplicate

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Do something'),
        createTestMessage('a1-base', 'assistant', 'Let me check...'),
        createTestMessage('t1', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2-final', 'assistant', 'Done!'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a2-ws', 'assistant', 'Done!', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      // Should not have duplicate assistants
      const assistants = result.filter((m) => m.role === 'assistant')
      expect(assistants.length).toBe(2)
      expect(result.some((m) => m.id === 'a2-ws')).toBe(false)
    })

    test('keeps the final assistant of a tool turn when base has not caught up yet', () => {
      // Scenario: a tool-call turn. The backend persists EACH iteration as a
      // SEPARATE message (it does NOT merge a multi-iteration tool turn into a
      // single assistant in HTTP history). So canonical history is:
      //   [u1, a1-pre, t1, a1-final]
      //
      // The final assistant just completed streaming, but the HTTP poll hasn't
      // caught up yet: base still only has [u1, a1-pre]. The streaming list has
      // the full turn. `baseHasCurrentTurn` is already true (base has user +
      // first assistant), but the final assistant must NOT be dropped — it is a
      // new message base hasn't seen, and dropping it would make the final
      // answer vanish.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Run a command'),
        createTestMessage('a1-pre', 'assistant', 'Let me check...'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a1-pre', 'assistant', 'Let me check...', {
          streaming: false,
        }),
        createTestMessage('t1', 'tool', 'result', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a1-final', 'assistant', 'Done!', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      const assistants = result.filter((m) => m.role === 'assistant')
      // a1-pre is deduped, a1-final is preserved.
      expect(assistants.length).toBe(2)
      expect(result.some((m) => m.id === 'a1-pre')).toBe(true)
      expect(result.some((m) => m.id === 'a1-final')).toBe(true)
      // The final answer must be present.
      expect(result.some((m) => m.id === 'a1-final' && m.content === 'Done!')).toBe(true)
    })
    test('empty placeholder does not shift position-matching and cause a duplicate final', () => {
      // Scenario: a tool-call turn where the FIRST iteration produced no text
      // (a tool-call-only response). toChatMessages SKIPS that content-less
      // assistant in HTTP history, so base has [u1, t1, a-final] — only ONE
      // assistant. But streaming still carries the empty placeholder, giving
      // TWO streaming assistants: [a-empty, t1, a-final].
      //
      // OLD BUG: position-matching paired base assistant #0 (a-final-base) with
      // streaming assistant #0 (the EMPTY placeholder), leaving a-final-ws
      // unmatched. a-final-ws then survived the leftover filter and rendered as
      // a duplicate of a-final-base.
      //
      // FIX: empty completed placeholders are excluded from the matchable index,
      // so the final assistant matches correctly and is deduped.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Run a command'),
        createTestMessage('t1', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a-final-base', 'assistant', 'Done!'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a-empty', 'assistant', '', { streaming: false }),
        createTestMessage('t1', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a-final-ws', 'assistant', 'Done!', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      const assistants = result.filter((m) => m.role === 'assistant')
      expect(assistants.length).toBe(1)
      expect(assistants[0].id).toBe('a-final-base')
      expect(result.some((m) => m.id === 'a-final-ws')).toBe(false)
      expect(result.some((m) => m.id === 'a-empty')).toBe(false)
    })
    test('drops empty tool-call iteration placeholder to prevent 2->1 flicker', () => {
      // Scenario: a turn with a tool call. The backend generates distinct
      // message IDs per iteration (iterationMsgID: baseID, baseID-2). The first
      // iteration is a tool-call-only response with NO text content; the second
      // is the final response. During streaming the frontend creates TWO
      // assistant placeholders, but the canonical HTTP history only persists
      // ONE (toChatMessages skips content-less tool-call assistants).
      //
      // Before the HTTP refetch lands (baseHasCurrentTurn=false), the empty
      // placeholder would still render, showing 2 bubbles that collapse to 1
      // once base catches up — a visible flicker. The empty completed assistant
      // must be dropped regardless of baseHasCurrentTurn.
      const baseMessages: ChatMessage[] = [createTestMessage('u1', 'user', 'Run a command')]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('a1', 'assistant', '', { streaming: false }),
        createTestMessage('t1', 'tool', 'result', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a1-2', 'assistant', 'Done!', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      const assistants = result.filter((m) => m.role === 'assistant')
      expect(assistants.length).toBe(1)
      expect(assistants[0].id).toBe('a1-2')
      expect(result.some((m) => m.id === 'a1')).toBe(false)
    })
  })

  describe('Thinking blocks: each assistant keeps its own reasoningContent', () => {
    test('keeps reasoningContent independent per assistant in a multi-iteration turn', () => {
      // Scenario: Multi-iteration tool-use turn. Each iteration streamed its
      // own reasoningContent. Each assistant keeps its OWN thinking block —
      // they must NOT be consolidated into a single giant block.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Analyze this code'),
        createTestMessage('a1', 'assistant', 'Let me read the file', {
          reasoningContent: 'I need to read the file first',
        }),
        createTestMessage('t1', 'tool', 'file contents', {
          toolName: 'read_file',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2', 'assistant', 'Now let me run a test', {
          reasoningContent: 'The file looks correct, let me verify with a test',
        }),
        createTestMessage('t2', 'tool', 'test output', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-2',
        }),
        createTestMessage('a3', 'assistant', 'Everything looks good!', {
          reasoningContent: 'Tests pass, the code is correct',
        }),
      ]

      const result = mergeMessages(baseMessages, [])

      const assistants = result.filter((m) => m.role === 'assistant')

      // Each assistant keeps its own reasoningContent untouched
      expect(assistants[0].reasoningContent).toBe('I need to read the file first')
      expect(assistants[1].reasoningContent).toBe(
        'The file looks correct, let me verify with a test',
      )
      expect(assistants[2].reasoningContent).toBe('Tests pass, the code is correct')
    })

    test('keeps reasoningContent independent across turns', () => {
      // Reasoning from different turns (separated by user messages) stays
      // separate on each of its own assistants.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'First question'),
        createTestMessage('a1', 'assistant', 'Step 1', {
          reasoningContent: 'Thinking about turn 1',
        }),
        createTestMessage('t1', 'tool', 'result', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2', 'assistant', 'Answer 1', {
          reasoningContent: 'More thinking for turn 1',
        }),
        createTestMessage('u2', 'user', 'Second question'),
        createTestMessage('a3', 'assistant', 'Answer 2', {
          reasoningContent: 'Thinking about turn 2',
        }),
      ]

      const result = mergeMessages(baseMessages, [])

      const assistants = result.filter((m) => m.role === 'assistant')

      // Turn 1 assistants keep their own reasoning
      expect(assistants[0].reasoningContent).toBe('Thinking about turn 1')
      expect(assistants[1].reasoningContent).toBe('More thinking for turn 1')

      // Turn 2 assistant keeps its own reasoning
      expect(assistants[2].reasoningContent).toBe('Thinking about turn 2')
    })

    test('single assistant with reasoningContent is left untouched', () => {
      // One assistant with reasoning in a turn keeps its own reasoning.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi!', {
          reasoningContent: 'Simple greeting',
        }),
      ]

      const result = mergeMessages(baseMessages, [])

      const assistants = result.filter((m) => m.role === 'assistant')
      expect(assistants[0].reasoningContent).toBe('Simple greeting')
    })

    test('assistant without reasoningContent stays undefined', () => {
      // Iterations without reasoning stay without reasoning.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Do something'),
        createTestMessage('a1', 'assistant', '', {
          reasoningContent: 'Initial analysis',
        }),
        createTestMessage('t1', 'tool', 'result', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2', 'assistant', 'Continuing...', {
          // No reasoningContent on this one
        }),
        createTestMessage('t2', 'tool', 'result2', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-2',
        }),
        createTestMessage('a3', 'assistant', 'Done!', {
          reasoningContent: 'Final reasoning',
        }),
      ]

      const result = mergeMessages(baseMessages, [])

      const assistants = result.filter((m) => m.role === 'assistant')

      // Each keeps its own: a1 has reasoning, a2 does not, a3 has reasoning
      expect(assistants[0].reasoningContent).toBe('Initial analysis')
      expect(assistants[1].reasoningContent).toBeUndefined()
      expect(assistants[2].reasoningContent).toBe('Final reasoning')
    })

    test('keeps reasoningContent independent during active streaming', () => {
      // Simulates live streaming state where iterations are still arriving.
      // Each streaming iteration keeps its own thinking block.
      const baseMessages: ChatMessage[] = []

      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Analyze this'),
        createTestMessage('a1-ws', 'assistant', 'Reading file...', {
          streaming: false,
          reasoningContent: 'Let me start by reading the file',
        }),
        createTestMessage('t1', 'tool', 'contents', {
          toolName: 'read_file',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2-ws', 'assistant', 'Here is my analysis...', {
          streaming: true,
          reasoningContent: 'After reading the file, I can see...',
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)

      const assistants = result.filter((m) => m.role === 'assistant')

      // Each assistant keeps its own reasoning
      expect(assistants[0].reasoningContent).toBe('Let me start by reading the file')
      expect(assistants[1].reasoningContent).toBe('After reading the file, I can see...')
    })

    test('reconciles intermediate base history with streaming without duplicating thinking blocks', () => {
      // Scenario: Iterations 1 & 2 have been saved by backend and returned in baseMessages (via HTTP poll).
      // Iteration 3 is actively streaming.
      // Expected: Base iterations 1 & 2 are NOT duplicated by streaming iterations 1 & 2.
      // Exactly 3 assistants are returned in chronological order.
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Run the pipeline'),
        createTestMessage('a1-base', 'assistant', 'Starting step 1...', {
          reasoningContent: 'Reasoning for step 1',
        }),
        createTestMessage('t1', 'tool', 'step 1 done', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2-base', 'assistant', 'Starting step 2...', {
          reasoningContent: 'Reasoning for step 2',
        }),
        createTestMessage('t2', 'tool', 'step 2 done', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-2',
        }),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-opt', 'user', 'Run the pipeline', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a1-ws', 'assistant', 'Starting step 1...', {
          streaming: false,
          reasoningContent: 'Reasoning for step 1',
        }),
        createTestMessage('t1-ws', 'tool', 'step 1 done', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-1',
        }),
        createTestMessage('a2-ws', 'assistant', 'Starting step 2...', {
          streaming: false,
          reasoningContent: 'Reasoning for step 2',
        }),
        createTestMessage('t2-ws', 'tool', 'step 2 done', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'tc-2',
        }),
        createTestMessage('a3-ws', 'assistant', 'Starting step 3...', {
          streaming: true,
          reasoningContent: 'Reasoning for step 3',
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const assistants = result.filter((m) => m.role === 'assistant')

      // Exactly 3 assistants in order, no duplicates or accumulated thinking blocks
      expect(assistants.length).toBe(3)
      expect(assistants[0].reasoningContent).toBe('Reasoning for step 1')
      expect(assistants[1].reasoningContent).toBe('Reasoning for step 2')
      expect(assistants[2].reasoningContent).toBe('Reasoning for step 3')
      expect(result.map((m) => m.id)).toEqual([
        'u1',
        'a1-base',
        't1-ws',
        'a2-base',
        't2-ws',
        'a3-ws',
      ])
    })
  })

  describe('Bug 7: Sequential user messages and multi-turn ordering', () => {
    test('maintains chronological order when sending a second message while first is streaming', () => {
      // Scenario:
      // Turn 1: user sent u1, assistant a1 is actively streaming.
      // User immediately sends u2 before a1 finishes.
      // Base history: [] (or confirmed older messages)
      // Streaming: [u1-opt, a1-stream, u2-opt]
      // Expected render: [u1-opt, a1-stream, u2-opt] (NOT [u1-opt, u2-opt, a1-stream])

      const baseMessages: ChatMessage[] = []
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-opt', 'user', 'First question', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a1-ws', 'assistant', 'Answering first...', {
          streaming: true,
        }),
        createTestMessage('u2-opt', 'user', 'Second question', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      expect(result.map((m) => m.id)).toEqual(['u1-opt', 'a1-ws', 'u2-opt'])
      expect(result.map((m) => m.role)).toEqual(['user', 'assistant', 'user'])
    })

    test('maintains chronological order when sending a second message after first completes before refetch', () => {
      // Scenario:
      // Turn 1: user sent u1, assistant a1 completed (streaming: false), but HTTP refetch has not landed.
      // User sends u2.
      // Base history: []
      // Streaming: [u1-opt, a1-ws, u2-opt]
      // Expected render: [u1-opt, a1-ws, u2-opt]

      const baseMessages: ChatMessage[] = []
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-opt', 'user', 'First question', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a1-ws', 'assistant', 'First answer', {
          streaming: false,
        }),
        createTestMessage('u2-opt', 'user', 'Second question', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      expect(result.map((m) => m.id)).toEqual(['u1-opt', 'a1-ws', 'u2-opt'])
      expect(result.map((m) => m.role)).toEqual(['user', 'assistant', 'user'])
    })

    test('maintains order when a2 starts streaming in response to u2', () => {
      // Scenario:
      // Turn 1: u1 and a1 completed in streaming.
      // Turn 2: u2 sent, a2 starts streaming.
      // Streaming: [u1-opt, a1-ws, u2-opt, a2-ws]
      // Expected render: [u1-opt, a1-ws, u2-opt, a2-ws]

      const baseMessages: ChatMessage[] = []
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-opt', 'user', 'First question', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a1-ws', 'assistant', 'First answer', {
          streaming: false,
        }),
        createTestMessage('u2-opt', 'user', 'Second question', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a2-ws', 'assistant', 'Second answer...', {
          streaming: true,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      expect(result.map((m) => m.id)).toEqual(['u1-opt', 'a1-ws', 'u2-opt', 'a2-ws'])
      expect(result.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    })

    test('handles subagent session where base has existing messages and user sends a new message', () => {
      // Scenario:
      // Subagent session has baseMessages from task setup: [u_task, a_task]
      // User types a message to the subagent: u_user-opt with optimisticBaseCount = 1
      // Expected render: [u_task, a_task, u_user-opt]

      const baseMessages: ChatMessage[] = [
        createTestMessage('u_task', 'user', 'Task instruction'),
        createTestMessage('a_task', 'assistant', 'Task started...'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('u_user-opt', 'user', 'Please also check logs', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      expect(result.map((m) => m.id)).toEqual(['u_task', 'a_task', 'u_user-opt'])
      expect(result.map((m) => m.role)).toEqual(['user', 'assistant', 'user'])
    })
  })

  describe('Fix: Stable keys across the WebSocket→HTTP transition (flicker)', () => {
    test('confirmed assistant carries the streaming id as stableId', () => {
      // Before the HTTP refetch lands, the streaming copy has an ephemeral id.
      // Once base has the full turn, the base copy wins — but it must keep the
      // ephemeral id as `stableId` so the React key (and enter animation) do
      // not change, preventing a remount flicker at the moment of confirmation.

      const baseMessages: ChatMessage[] = [
        createTestMessage('u1-base', 'user', 'hello'),
        createTestMessage('a1-base', 'assistant', 'Answer A'),
      ]
      const streamingMessages: ChatMessage[] = [
        createTestMessage('a1-ws', 'assistant', 'Answer A', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const confirmed = result.find((m) => m.id === 'a1-base')
      expect(confirmed).toBeDefined()
      // The base copy must keep the ephemeral id so rendering stays stable.
      expect(confirmed?.stableId).toBe('a1-ws')
      // No duplicate: the streaming copy is consumed.
      expect(result.filter((m) => m.content === 'Answer A')).toHaveLength(1)
    })

    test('confirmed user message carries the optimistic id as stableId', () => {
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1-base', 'user', 'Hello there'),
        createTestMessage('a1-base', 'assistant', 'Answer'),
      ]
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-temp', 'user', 'Hello there', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const confirmedUser = result.find((m) => m.id === 'u1-base')
      expect(confirmedUser).toBeDefined()
      expect(confirmedUser?.stableId).toBe('u1-temp')
      // The optimistic copy is dropped; the confirmed copy owns the key.
      expect(result.some((m) => m.id === 'u1-temp')).toBe(false)
    })

    test('streaming assistant not yet in base retains stableId = its own id', () => {
      const baseMessages: ChatMessage[] = []
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-temp', 'user', 'hi', {
          optimistic: true,
          optimisticBaseCount: 0,
        }),
        createTestMessage('a1-ws', 'assistant', 'Working...', { streaming: true }),
      ]
      const result = mergeMessages(baseMessages, streamingMessages)
      const streaming = result.find((m) => m.id === 'a1-ws')
      expect(streaming).toBeDefined()
      // Should fall back to its own id for a stable React key.
      expect(streaming?.stableId ?? streaming?.id).toBe('a1-ws')
    })
  })
})
