import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { mergeMessages } from './useChatHistory'

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
        'user', 'assistant', 'tool', 'assistant', 'tool', 'assistant',
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
  })
})
