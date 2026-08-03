/**
 * Shared helper utilities for message event handlers.
 *
 * Small pure functions that multiple domain modules depend on:
 * session-key extraction, mismatch detection, tool-message lookup,
 * and GroupSnapshot → GroupInfo conversion.
 */
import type { ChatMessage, GroupInfo, GroupSnapshot } from '../../lib/types'

/** Convert a GroupSnapshot (from WS/HTTP) into the internal GroupInfo shape. */
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

export function isSessionMismatch(
  eventSessionKey: string | undefined,
  currentSessionKey: string | null,
  label: string,
): boolean {
  if (eventSessionKey && eventSessionKey !== currentSessionKey) {
    console.warn(`[WS] Dropped ${label}: session mismatch`, {
      eventSessionKey,
      currentSessionKey,
    })
    return true
  }
  return false
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
