import { useMemo } from 'react'
import type { ChatMessage } from '../lib/types'

/**
 * Provides O(1) lookup indexes over the streaming messages array.
 * The indexes are memoized and only recomputed when the array reference changes.
 *
 * Usage:
 *   const index = useMessageIndex(streamingMessages)
 *   const idx = index.indexOf(messageId)       // O(1)
 *   const last = index.lastAssistantIndex      // O(1)
 */
export function useMessageIndex(messages: ChatMessage[]) {
  return useMemo(() => {
    const byId = new Map<string, number>()
    let lastAssistantIdx = -1
    let lastToolIdx = -1

    for (let i = 0; i < messages.length; i++) {
      const msg = messages[i]
      byId.set(msg.id, i)
      if (msg.role === 'assistant') {
        lastAssistantIdx = i
      }
      if (msg.role === 'tool') {
        lastToolIdx = i
      }
    }

    return {
      /** O(1) lookup: message ID → array index (-1 if not found) */
      indexOf: (id: string): number => byId.get(id) ?? -1,
      /** Index of the last assistant message, or -1 */
      lastAssistantIndex: lastAssistantIdx,
      /** Index of the last tool message, or -1 */
      lastToolIndex: lastToolIdx,
      /** Total message count */
      size: messages.length,
    }
  }, [messages])
}
