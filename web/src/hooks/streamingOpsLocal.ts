import type { ChatMessage } from '../lib/types'

export function finalizeStreamingAssistantsForSession(
  current: ChatMessage[],
  sessionKey: string | null | undefined,
): ChatMessage[] {
  if (!sessionKey) return current
  let changed = false
  const next = current.map((m) => {
    if (m.sessionKey === sessionKey && m.role === 'assistant' && m.streaming) {
      changed = true
      return { ...m, streaming: false }
    }
    return m
  })
  return changed ? next : current
}

export function hasStreamingMessageForSession(
  current: ChatMessage[],
  sessionKey: string | null | undefined,
): boolean {
  if (!sessionKey) return false
  return current.some((m) => m.streaming && m.sessionKey === sessionKey)
}
