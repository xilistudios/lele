import type { ChatMessage, GroupInfo, GroupSnapshot } from '../../lib/types'
import type { MessageEventContext } from './types'

export function snapshotToGroupInfo(s: GroupSnapshot): GroupInfo {
  return {
    groupID: s.group_id,
    status: s.status,
    strategy: s.strategy,
    participants: s.participants,
    layers: s.layers,
    totalTokens: s.total_tokens,
    createdAt: s.created_at || new Date().toISOString(),
    synthesis: s.synthesis || undefined,
    turns: s.turns.map((t) => ({
      groupID: s.group_id,
      speaker: t.speaker,
      label: t.label,
      role: t.role,
      layer: t.layer,
      turnIndex: t.turn_index,
      content: t.content,
      toolCalls: t.tool_calls,
    })),
  }
}

export function getSessionKey(data: Record<string, unknown>): string | undefined {
  return data.session_key as string | undefined
}

const CONVERSATION_ALIAS_SUFFIX = /:chat:\d+$/

function stripConversationAlias(sessionKey: string): string {
  return sessionKey.replace(CONVERSATION_ALIAS_SUFFIX, '')
}

export function sessionKeysLooselyMatch(a?: string | null, b?: string | null): boolean {
  if (!a || !b) return false
  if (a === b) return true
  return stripConversationAlias(a) === stripConversationAlias(b)
}

export function isSessionMismatch(
  eventSessionKey: string | undefined,
  currentSessionKey: string | null,
  label: string,
): boolean {
  if (
    eventSessionKey &&
    currentSessionKey &&
    !sessionKeysLooselyMatch(eventSessionKey, currentSessionKey)
  ) {
    console.warn(`[WS] Dropped ${label}: session mismatch`, {
      eventSessionKey,
      currentSessionKey,
    })
    return true
  }
  return false
}

export function effectiveSessionKey(
  ctx: Pick<MessageEventContext, 'currentSessionKeyRef'>,
  eventSessionKey?: string,
): string {
  const current = ctx.currentSessionKeyRef.current
  if (!eventSessionKey) return current ?? ''
  if (current && sessionKeysLooselyMatch(eventSessionKey, current)) return current
  return eventSessionKey
}

export function findToolMessageIndex(
  messages: ChatMessage[],
  toolCallId: string | undefined,
  fallback: (messages: ChatMessage[]) => number,
): number {
  if (toolCallId) {
    const idx = messages.findIndex((m) => m.role === 'tool' && m.toolCallId === toolCallId)
    if (idx >= 0) return idx
  }
  return fallback(messages)
}
