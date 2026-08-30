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

/**
 * Conversation-alias suffix the backend appends after `/new` or `/agent`
 * (pkg/agent/loop.go `nextConversationSessionKey` builds `base + ":chat:" + N`
 * and `sessionAliases.Store(base, aliased)`).
 */
const CONVERSATION_ALIAS_SUFFIX = /:chat:\d+$/

/** Strip one trailing `:chat:<digits>` conversation-alias suffix. */
function stripConversationAlias(sessionKey: string): string {
  return sessionKey.replace(CONVERSATION_ALIAS_SUFFIX, '')
}

/**
 * True if two session keys refer to the same conversation, tolerating the
 * backend's conversation aliases: after /new or /agent the backend maps
 * `base` -> `base:chat:N` (pkg/agent/loop.go). Events (stream/complete) carry
 * the aliased key while the frontend only knows `base`. Two keys loosely match
 * when they are equal, or when stripping a trailing `:chat:<digits>` suffix
 * from both leaves the same root.
 *
 * Stripping from BOTH sides (instead of only testing whether one key is a
 * suffixed copy of the other) also matches two turns of the same base
 * conversation (`base:chat:1` vs `base:chat:5`): they share the same WebSocket
 * client and the same UI pane, so treating them as one UI session is correct.
 *
 * NOTE: alias↔alias matching is correct for the current single chat-pane model
 * (one client, one visible conversation per base key). Revisit this if the UI
 * ever supports parallel sub-conversations (`:chat:N` panes shown side by side)
 * — then two different aliases must NOT be treated as the same session.
 */
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
  // Drop only when BOTH keys exist and refer to different conversations. An
  // aliased key (`base:chat:N`) belongs to the current session and must flow
  // through — otherwise its placeholder never completes and the loading spinner
  // stays stuck (see handleMessageComplete).
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
