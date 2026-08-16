/**
 * Pure message-merge logic.
 *
 * The UI has TWO sources of truth for chat messages:
 *
 *   1. `baseMessages`  — canonical history from the HTTP API, cached by
 *      react-query. Message IDs are content-hash based and stable.
 *   2. `streamingMessages` — live state driven by WebSocket events
 *      (chunks, tool calls, acks). Message IDs are ephemeral UUIDs.
 *
 * Because the two sources use DIFFERENT id schemes for the same logical
 * message, we cannot match them by id. Instead we match ASSISTANTS by
 * POSITION: the Nth streaming assistant corresponds to the Nth-from-the-end
 * assistant in base (the most recent turn). `mergeMessages` reconciles the
 * two lists into a single render-ready array with no duplicates and correct
 * chronological order.
 *
 * Every rule below protects against a specific bug that was hit in
 * production. The rules are covered by messageOrdering.test.ts — keep that
 * suite green when changing anything here.
 */
import type { ChatMessage } from '../lib/types'

// ── Indexes over the two input lists ────────────────────────────────────────

/** A streaming assistant plus the flags we mutate while matching. */
type AssistantEntry = {
  msg: ChatMessage
  /** True while the message is still actively streaming (not yet completed). */
  isStreaming: boolean
  /** Set to true once this entry has been matched to a base assistant. */
  used: boolean
}

type StreamingIndex = {
  assistants: AssistantEntry[]
  toolCallIds: Set<string>
  toolSessions: Set<string>
  toolByCallId: Map<string, ChatMessage>
  /** Optimistic user messages (content-matched against base on confirmation). */
  optimisticUsers: Array<{ msg: ChatMessage; used: boolean }>
}

/** Index the streaming list once so the merge can run in linear time. */
function indexStreaming(streamingMessages: ChatMessage[]): StreamingIndex {
  const assistants: AssistantEntry[] = []
  const toolCallIds = new Set<string>()
  const toolSessions = new Set<string>()
  const toolByCallId = new Map<string, ChatMessage>()
  const optimisticUsers: Array<{ msg: ChatMessage; used: boolean }> = []

  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
      assistants.push({ msg, isStreaming: msg.streaming === true, used: false })
    } else if (msg.role === 'tool') {
      if (msg.toolCallId) {
        toolCallIds.add(msg.toolCallId)
        toolByCallId.set(msg.toolCallId, msg)
      }
      if (msg.sessionKey) {
        toolSessions.add(msg.sessionKey)
      }
    } else if (msg.role === 'user' && msg.optimistic) {
      optimisticUsers.push({ msg, used: false })
    }
  }

  return { assistants, toolCallIds, toolSessions, toolByCallId, optimisticUsers }
}

// ── Turn detection ──────────────────────────────────────────────────────────

/**
 * Whether the HTTP base already contains the FULL current turn — i.e. both
 * the user message AND the assistant response.
 *
 * Guarded by `baseAssistantCount >= baseUserCountNonOptimistic`: without it,
 * the flag flipped true as soon as the optimistic user message landed in the
 * query cache (before the assistant existed in base), which made the matcher
 * pair the new streaming assistant with an OLD base assistant and render the
 * response ABOVE the user message.
 */
function computeBaseHasCurrentTurn(
  baseMessages: ChatMessage[],
  streamingMessages: ChatMessage[],
): boolean {
  const baseUserCountNonOptimistic = baseMessages.filter(
    (m) => m.role === 'user' && !m.optimistic,
  ).length
  const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length
  const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)

  return (
    baseUserCountNonOptimistic > (optimisticUser?.optimisticBaseCount ?? 0) &&
    baseAssistantCount >= baseUserCountNonOptimistic
  )
}

/**
 * Offset into the base assistant list where position-matching begins.
 *
 * Streaming assistants always correspond to the LAST N assistants in base
 * (the most recent turn). When the base hasn't caught up with the current
 * turn, no matching should occur at all, so the offset equals the total
 * number of base assistants (nothing will be reached).
 */
function computeMatchOffset(
  baseAssistantCount: number,
  streamingAssistantCount: number,
  baseHasCurrentTurn: boolean,
): number {
  return baseHasCurrentTurn
    ? Math.max(0, baseAssistantCount - streamingAssistantCount)
    : baseAssistantCount
}

// ── Base pass ───────────────────────────────────────────────────────────────

type BasePassResult = {
  /** Base messages, with tool messages swapped for their live streaming copy. */
  filteredBase: ChatMessage[]
  /** Streaming tool_call_ids that were consumed (placed) into filteredBase. */
  consumedToolIds: Set<string>
}

/**
 * Walk the base list, preserving canonical server order, while:
 *   - marking matched (completed) streaming assistants as `used`, and
 *   - replacing base tool messages with their live streaming copy in-place.
 *
 * Actively-streaming assistants are intentionally NOT matched here: they are
 * new messages that don't exist in base yet, so replacing an older base
 * assistant in-place would break chronological order. They are appended later.
 */
function buildFilteredBase(
  baseMessages: ChatMessage[],
  streamingMessages: ChatMessage[],
  index: StreamingIndex,
  matchOffset: number,
): BasePassResult {
  const consumedToolIds = new Set<string>()
  const filteredBase: ChatMessage[] = []
  let baseAssistantIdx = 0
  let streamAsstIdx = 0
  let optimisticUserIdx = 0

  for (const msg of baseMessages) {
    if (msg.role === 'assistant') {
      let stableId: string | undefined
      let confirmedAlready = false
      if (baseAssistantIdx >= matchOffset && streamAsstIdx < index.assistants.length) {
        const entry = index.assistants[streamAsstIdx]
        if (!entry.isStreaming) {
          // Both completed → base version wins; remember the streaming copy
          // was consumed so it gets deduped later. Carry its stable id forward
          // so the render key (and enter animation) survive the transition.
          entry.used = true
          stableId = entry.msg.stableId ?? entry.msg.id
          confirmedAlready = true
          streamAsstIdx++
        }
        // If entry.isStreaming, skip it (leave it for the append pass).
      }
      baseAssistantIdx++
      filteredBase.push(confirmedAlready && stableId ? { ...msg, stableId } : msg)
      continue
    }

    if (msg.role === 'user' && !msg.optimistic) {
      // A confirmed user message: if it matches an optimistic user that is
      // being dropped, carry the optimistic id forward as stableId so the
      // sent bubble doesn't remount/re-animate when the real copy lands.
      let stableId: string | undefined
      for (; optimisticUserIdx < index.optimisticUsers.length; optimisticUserIdx++) {
        const optUser = index.optimisticUsers[optimisticUserIdx]
        if (optUser.used) continue
        if (optUser.msg.content === msg.content) {
          optUser.used = true
          stableId = optUser.msg.stableId ?? optUser.msg.id
          break
        }
      }
      filteredBase.push(stableId ? { ...msg, stableId } : msg)
      continue
    }

    if (msg.role === 'tool' && msg.toolCallId && index.toolCallIds.has(msg.toolCallId)) {
      const streamingTool = index.toolByCallId.get(msg.toolCallId)
      if (streamingTool) {
        filteredBase.push(streamingTool)
        consumedToolIds.add(msg.toolCallId)
      } else {
        filteredBase.push(msg)
      }
      continue
    }

    if (
      msg.role === 'tool' &&
      !msg.toolCallId &&
      msg.sessionKey &&
      index.toolSessions.has(msg.sessionKey) &&
      streamingMessages.some((sm) => sm.role === 'tool' && sm.sessionKey === msg.sessionKey)
    ) {
      // Base tool without a call-id that a streaming tool supersedes → drop.
      continue
    }

    filteredBase.push(msg)
  }

  return { filteredBase, consumedToolIds }
}

// ── Streaming pass ──────────────────────────────────────────────────────────

/**
 * Filter the streaming list down to messages not yet represented in base.
 *
 * Drops:
 *   - optimistic user messages once base has caught up,
 *   - assistants already matched into filteredBase (`used`),
 *   - completed assistants left over from a previous turn once base has the
 *     full current turn (the backend merges a tool-call turn into a single
 *     assistant in HTTP history),
 *   - empty completed assistants with no reasoning content (placeholders),
 *   - tool messages already confirmed in base.
 */
function filterStreamingLeftovers(
  streamingMessages: ChatMessage[],
  index: StreamingIndex,
  baseMessages: ChatMessage[],
  filteredBase: ChatMessage[],
  consumedToolIds: Set<string>,
  baseHasCurrentTurn: boolean,
): ChatMessage[] {
  const baseUserCount = baseMessages.filter((m) => m.role === 'user' && !m.optimistic).length
  const usedAssistantIds = new Set(index.assistants.filter((e) => e.used).map((e) => e.msg.id))

  return streamingMessages.filter((msg) => {
    if (msg.role === 'user') {
      if (!msg.optimistic) return true
      // Keep the optimistic user until base has more users than it did when
      // the message was sent (i.e. the confirmed copy hasn't landed yet).
      return baseUserCount <= (msg.optimisticBaseCount ?? 0)
    }

    if (msg.role === 'assistant') {
      if (usedAssistantIds.has(msg.id)) return false
      if (!msg.streaming && baseHasCurrentTurn) {
        // Completed but unmatched → leftover from a previous turn. Drop it.
        if (index.assistants.some((e) => e.msg.id === msg.id)) return false
      }
      if (!msg.streaming && msg.content.trim() === '' && !msg.reasoningContent) {
        // Empty tool-call iteration placeholder without reasoning — drop.
        return false
      }
      return true
    }

    if (msg.role === 'tool') {
      if (msg.toolCallId && consumedToolIds.has(msg.toolCallId)) return false
      if (msg.toolCallId) {
        const confirmed = filteredBase.some(
          (bm) => bm.role === 'tool' && bm.toolCallId === msg.toolCallId,
        )
        if (confirmed) return false
      }
      return true
    }

    return true
  })
}

// ── Entry point ─────────────────────────────────────────────────────────────

/**
 * Merge canonical HTTP history with live WebSocket streaming state into a
 * single, deduplicated, chronologically-ordered message list for rendering.
 */
/**
 * Merge canonical HTTP history with live WebSocket streaming state into a
 * single, deduplicated, chronologically-ordered message list for rendering.
 *
 * Each LLM iteration streams under its own message ID (the backend appends a
 * `-<iteration>` suffix), so every assistant message carries its OWN
 * `reasoningContent`. We intentionally do NOT consolidate reasoning across
 * iterations into a single block: each turn keeps its own independent
 * "Thinking..." block so a multi-step turn doesn't grow into one giant one.
 */
export function mergeMessages(
  baseMessages: ChatMessage[],
  streamingMessages: ChatMessage[],
): ChatMessage[] {
  const index = indexStreaming(streamingMessages)
  const baseHasCurrentTurn = computeBaseHasCurrentTurn(baseMessages, streamingMessages)
  const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length
  const matchOffset = computeMatchOffset(
    baseAssistantCount,
    index.assistants.length,
    baseHasCurrentTurn,
  )

  const { filteredBase, consumedToolIds } = buildFilteredBase(
    baseMessages,
    streamingMessages,
    index,
    matchOffset,
  )

  const filteredStreaming = filterStreamingLeftovers(
    streamingMessages,
    index,
    baseMessages,
    filteredBase,
    consumedToolIds,
    baseHasCurrentTurn,
  )

  return [...filteredBase, ...filteredStreaming]
}
