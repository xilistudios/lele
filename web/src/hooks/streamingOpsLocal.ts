/**
 * Local pure transitions used by the processing-indicator safety nets.
 *
 * These live in their own file (instead of `streamingOps.ts` or the
 * event-handler modules) so the loading-stuck backstops in `useAppLogic`
 * and `handleSubscribeAck` can be unit-tested without React and without
 * coupling to the WebSocket handler layer.
 *
 * All functions are pure: (current, ...args) => next, and they return the
 * SAME array reference when nothing changes so `setStreamingMessages` stays
 * a no-op for React.
 */
import type { ChatMessage } from '../lib/types'

/**
 * Finalize (streaming → false) every assistant message belonging to
 * `sessionKey`. Other sessions and non-assistant roles are left untouched.
 *
 * Used when the backend is known to no longer be processing a session
 * (HTTP poll true→false transition, subscribe.ack with processing:false):
 * any remaining `streaming: true` assistant for that session is stale and
 * would otherwise keep the loading dots lit until the page is reloaded.
 */
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

/**
 * True if any streaming message belongs to `sessionKey`.
 *
 * The loading indicator must be scoped per session: an orphan placeholder
 * left over from another conversation (background session, missed
 * message.complete) must not light up the dots of the chat being viewed.
 */
export function hasStreamingMessageForSession(
  current: ChatMessage[],
  sessionKey: string | null | undefined,
): boolean {
  if (!sessionKey) return false
  return current.some((m) => m.streaming && m.sessionKey === sessionKey)
}
