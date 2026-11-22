import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { countPrependedItems, nextFirstItemIndex } from './messageInsertion'
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

      const baseUserCount = baseMessages.filter((m) => m.role === 'user').length
      const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length
      const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)

      const baseHasCurrentTurn =
        baseUserCount > (optimisticUser?.optimisticBaseCount ?? 0) &&
        baseAssistantCount >= baseUserCount

      expect(baseHasCurrentTurn).toBe(false)

      const matchOffset = baseHasCurrentTurn
        ? Math.max(0, baseAssistantCount - 1)
        : baseAssistantCount

      expect(matchOffset).toBe(2)
    })

    test('baseHasCurrentTurn should be true when base has both user and assistant', () => {
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

      const baseUserCount = baseMessages.filter((m) => m.role === 'user').length
      const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length
      const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)

      const baseHasCurrentTurn =
        baseUserCount > (optimisticUser?.optimisticBaseCount ?? 0) &&
        baseAssistantCount >= baseUserCount

      expect(baseHasCurrentTurn).toBe(true)
    })
  })

  describe('Bug 2: Tool calls should be in chronological order', () => {
    test('tool insertion should maintain chronological order', () => {
      let current: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi', { streaming: true }),
      ]

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

      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't1', 't2'])

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

      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't1', 't2', 't3'])
    })

    test('old buggy logic would produce reverse order', () => {
      let current: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi', { streaming: true }),
      ]

      const tool1 = createTestMessage('t1', 'tool', '', {
        toolName: 'read_file',
        toolStatus: 'executing',
      })

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

      expect(current.map((m) => m.id)).toEqual(['u1', 'a1', 't2', 't1'])
    })
  })

  describe('Bug 4: Streaming text should appear after tool calls, not before', () => {
    test('new assistant inserted before trailing tools when tools arrive first', () => {
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

      const newMsg = createTestMessage('a1', 'assistant', 'Let me check...', {
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

      expect(arr.map((m) => m.id)).toEqual(['a1', 't1', 't2'])
      expect(arr[0].role).toBe('assistant')
      expect(arr[1].role).toBe('tool')
      expect(arr[2].role).toBe('tool')
    })

    test('assistant appended after existing assistant and its tools', () => {
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

      expect(arr.map((m) => m.id)).toEqual(['a1', 't1', 't2', 'a2'])
    })

    test('assistant appended at end when no tools exist', () => {
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
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'Hello'),
        createTestMessage('a1', 'assistant', 'Hi!'),
        createTestMessage('u2', 'user', 'How are you?'),
        createTestMessage('u2-opt', 'user', 'How are you?', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
        createTestMessage('a2-base', 'assistant', 'Doing great!'),
      ]

      const streamingMessages: ChatMessage[] = [
        createTestMessage('u2-opt', 'user', 'How are you?', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
        createTestMessage('a2-ws', 'assistant', 'Doing great!', { streaming: false }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const assistantMessages = result.filter((m) => m.role === 'assistant')

      expect(assistantMessages.length).toBe(2)
      expect(assistantMessages[1].id).toBe('a2-base')
      expect(result.some((m) => m.id === 'a2-ws')).toBe(false)
    })
  })

  describe('Bug 6: New assistant inserted before optimistic user when prior assistant is retained', () => {
    test('new assistant should go AFTER the optimistic user, not after the retained assistant', () => {
      const current: ChatMessage[] = [
        createTestMessage('a1', 'assistant', 'First response', { streaming: false }),
        createTestMessage('u2-opt', 'user', 'Second question', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
      ]

      const newMsg = createTestMessage('a2', 'assistant', '', { streaming: true })

      const arr = [...current]
      arr.splice(computeAssistantInsertIndex(current), 0, newMsg)

      expect(arr.map((m) => m.id)).toEqual(['a1', 'u2-opt', 'a2'])
      expect(arr.map((m) => m.role)).toEqual(['assistant', 'user', 'assistant'])
    })

    test('continuation after tool calls still works when no user is after assistant', () => {
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

      expect(arr.map((m) => m.id)).toEqual(['u1-opt', 'a1', 't1', 't2', 'a2'])
    })
  })

  describe('Bug 5: Actively streaming assistant should not replace older base assistant in-place', () => {
    test('streaming assistant (post-tool-call) should be appended, not inserted at old position', () => {
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

      expect(result.map((m) => m.id)).toEqual(['u1', 'a1-base', 't1', 'a2-stream'])

      expect(result.map((m) => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    })

    test('multiple streaming assistants after tool calls maintain order', () => {
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

      const assistants = result.filter((m) => m.role === 'assistant')
      expect(assistants.length).toBe(2)
      expect(result.some((m) => m.id === 'a2-ws')).toBe(false)
    })

    test('keeps the final assistant of a tool turn when base has not caught up yet', () => {
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
      expect(assistants.length).toBe(2)
      expect(result.some((m) => m.id === 'a1-pre')).toBe(true)
      expect(result.some((m) => m.id === 'a1-final')).toBe(true)
      expect(result.some((m) => m.id === 'a1-final' && m.content === 'Done!')).toBe(true)
    })
    test('empty placeholder does not shift position-matching and cause a duplicate final', () => {
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

      expect(assistants[0].reasoningContent).toBe('I need to read the file first')
      expect(assistants[1].reasoningContent).toBe(
        'The file looks correct, let me verify with a test',
      )
      expect(assistants[2].reasoningContent).toBe('Tests pass, the code is correct')
    })

    test('keeps reasoningContent independent across turns', () => {
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

      expect(assistants[0].reasoningContent).toBe('Thinking about turn 1')
      expect(assistants[1].reasoningContent).toBe('More thinking for turn 1')

      expect(assistants[2].reasoningContent).toBe('Thinking about turn 2')
    })

    test('single assistant with reasoningContent is left untouched', () => {
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
        createTestMessage('a2', 'assistant', 'Continuing...', {}),
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

      expect(assistants[0].reasoningContent).toBe('Initial analysis')
      expect(assistants[1].reasoningContent).toBeUndefined()
      expect(assistants[2].reasoningContent).toBe('Final reasoning')
    })

    test('keeps reasoningContent independent during active streaming', () => {
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

      expect(assistants[0].reasoningContent).toBe('Let me start by reading the file')
      expect(assistants[1].reasoningContent).toBe('After reading the file, I can see...')
    })

    test('reconciles intermediate base history with streaming without duplicating thinking blocks', () => {
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
      expect(confirmed?.stableId).toBe('a1-ws')
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
      expect(streaming?.stableId ?? streaming?.id).toBe('a1-ws')
    })
  })

  describe('Fix: historical tools must not vanish when a new tool starts', () => {
    test('base tools without toolCallId are kept while a streaming tool runs', () => {
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'hello'),
        createTestMessage('a1', 'assistant', 'answer'),
        createTestMessage('t-old', 'tool', 'old result', {
          toolName: 'read_file',
          toolStatus: 'completed',
        }),
      ]
      const streamingMessages: ChatMessage[] = [
        createTestMessage('t-new-ws', 'tool', '', {
          toolName: 'exec',
          toolStatus: 'executing',
          toolCallId: 'call_new',
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      expect(result.some((m) => m.id === 't-old')).toBe(true)
      expect(result.some((m) => m.id === 't-new-ws')).toBe(true)
    })

    test('streaming tool confirmed in base is swapped in-place with stableId', () => {
      const baseMessages: ChatMessage[] = [
        createTestMessage('u1', 'user', 'hello'),
        createTestMessage('a1', 'assistant', 'answer'),
        createTestMessage('t-base', 'tool', 'result', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'call_1',
        }),
      ]
      const streamingMessages: ChatMessage[] = [
        createTestMessage('t-ws', 'tool', 'result', {
          toolName: 'exec',
          toolStatus: 'completed',
          toolCallId: 'call_1',
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const tools = result.filter((m) => m.role === 'tool')
      expect(tools).toHaveLength(1)
      expect(tools[0].id).toBe('t-ws')
      expect(tools[0].stableId ?? tools[0].id).toBe('t-ws')
    })
  })

  describe('Fix: rapid optimistic user sends stay visible and ordered', () => {
    test('confirming the first of two rapid sends does not drop the second', () => {
      const baseMessages: ChatMessage[] = [
        createTestMessage('u0', 'user', 'first ever'),
        createTestMessage('a0', 'assistant', 'reply 0'),
        createTestMessage('u1-base', 'user', 'hello'),
      ]
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u1-opt', 'user', 'hello', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
        createTestMessage('u2-opt', 'user', 'world', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      const userTexts = result.filter((m) => m.role === 'user').map((m) => m.content)
      expect(userTexts).toContain('hello')
      expect(userTexts).toContain('world')
      expect(result.some((m) => m.id === 'u2-opt')).toBe(true)
      expect(userTexts.filter((t) => t === 'hello')).toHaveLength(1)
    })

    test('repeated identical user text is only dropped after count grows', () => {
      const baseMessages: ChatMessage[] = [
        createTestMessage('u-old', 'user', 'ping'),
        createTestMessage('a-old', 'assistant', 'pong'),
        createTestMessage('u-new', 'user', 'ping'),
      ]
      const streamingMessages: ChatMessage[] = [
        createTestMessage('u-new-opt', 'user', 'ping', {
          optimistic: true,
          optimisticBaseCount: 1,
        }),
      ]

      const result = mergeMessages(baseMessages, streamingMessages)
      expect(result.some((m) => m.id === 'u-new-opt')).toBe(false)
      expect(result.filter((m) => m.role === 'user' && m.content === 'ping')).toHaveLength(2)
      expect(result.some((m) => m.id === 'u-new')).toBe(true)
    })
  })

  describe('Fix: Virtuoso firstItemIndex prepend detection (scroll flicker)', () => {
    test('true prepend of N items reports N', () => {
      expect(countPrependedItems('a', ['x', 'y', 'a', 'b'])).toBe(2)
      expect(countPrependedItems('a', ['a', 'b'])).toBe(0)
    })

    test('append at the end does not look like a prepend', () => {
      expect(countPrependedItems('old-first', ['old-first', 'a2', 'a3'])).toBe(0)
      expect(countPrependedItems(undefined, ['a', 'b'])).toBe(0)
    })

    test('id rewrite without a real prepend reports 0', () => {
      expect(countPrependedItems('temp-uuid', ['hash-1', 'hash-2', 'hash-3'])).toBe(0)
    })

    test('stableId-keyed first item tracks a real loadMore prepend', () => {
      const before = ['stable-a', 'stable-b']
      const after = ['older-1', 'older-2', 'stable-a', 'stable-b']
      expect(countPrependedItems(before[0], after)).toBe(2)
    })

    test('no shift when the previous first key is still first', () => {
      const index = 10000
      expect(nextFirstItemIndex(index, 'a', ['a', 'b', 'c'])).toBe(index)
      expect(nextFirstItemIndex(index, undefined, ['a', 'b'])).toBe(index)
    })

    test('shift by N when the previous first key moved to index N', () => {
      expect(nextFirstItemIndex(10000, 'a', ['x', 'y', 'a', 'b'])).toBe(9998)
      expect(nextFirstItemIndex(10000, 'a', ['x', 'a'])).toBe(9999)
    })

    test('no shift when the key is gone (id rewrite / collapse of the first row)', () => {
      expect(nextFirstItemIndex(10000, 'temp-uuid', ['hash-1', 'hash-2'])).toBe(10000)
      expect(nextFirstItemIndex(10000, 'thinking-1', ['thinking-2', 'a1'])).toBe(10000)
    })

    test('shift is measured on the COLLAPSED list, not on the merge output', () => {
      const mergeKeysBefore = ['a1', 'a2']
      const mergeKeysAfter = ['t1', 't2', 't3', 'u1', 'a0', 't4', 't5', ...mergeKeysBefore]
      expect(countPrependedItems(mergeKeysBefore[0], mergeKeysAfter)).toBe(7)

      const renderedKeys = ['t1', 'u1', 'a0', 't4', 't5', ...mergeKeysBefore]
      expect(countPrependedItems(mergeKeysBefore[0], renderedKeys)).toBe(5)
      expect(nextFirstItemIndex(10000, mergeKeysBefore[0], renderedKeys)).toBe(9995)
    })

    test('repeated prepends accumulate', () => {
      let index = 10000
      let prevFirstKey: string | undefined = 'a1'

      index = nextFirstItemIndex(index, prevFirstKey, ['p1', 'p2', 'a1'])
      expect(index).toBe(9998)
      prevFirstKey = 'p1'

      index = nextFirstItemIndex(index, prevFirstKey, ['q1', 'p1', 'p2', 'a1'])
      expect(index).toBe(9997)
      prevFirstKey = 'q1'

      index = nextFirstItemIndex(index, prevFirstKey, ['r1', 'r2', 'r3', 'q1', 'p1'])
      expect(index).toBe(9994)
    })

    test('never produces a negative index', () => {
      expect(nextFirstItemIndex(2, 'a', ['x', 'y', 'z', 'a'])).toBe(0)
      expect(nextFirstItemIndex(0, 'a', ['x', 'a'])).toBe(0)
      expect(nextFirstItemIndex(-5, 'a', ['x', 'a'])).toBe(0)
      expect(nextFirstItemIndex(-5, 'a', ['a'])).toBe(-5)
    })
  })
})
