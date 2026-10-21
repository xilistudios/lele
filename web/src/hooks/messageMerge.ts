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
 * message, we cannot match them by id. Assistants are therefore matched by
 * CONTENT IDENTITY (lib/messageIdentity.ts): the persisted copy and its live
 * twin are the same text. `mergeMessages` reconciles the two lists into a
 * single render-ready array with no duplicates and correct chronological order.
 *
 * Positional matching ("the Nth-from-the-end live assistant is the Nth-from-the-end
 * base assistant") used to be the rule here. It was removed because its count
 * invariant cannot hold in the real event stream — see `findStreamingTwin` for
 * the two conditions that break it and for the re-ordering it caused in
 * production. Never reintroduce it: a merge must not depend on how many live
 * copies happen to still be around.
 *
 * Every rule below protects against a specific bug that was hit in
 * production. The rules are covered by messageOrdering.test.ts,
 * messageOrderingWindow.test.ts and messagePairing.test.ts — keep those suites
 * green when changing anything here.
 */
import { assistantIdentityText } from '../lib/messageIdentity'
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
  toolByCallId: Map<string, ChatMessage>
  /** Optimistic user messages (content-matched against base on confirmation). */
  optimisticUsers: Array<{ msg: ChatMessage; used: boolean }>
}

/** Index the streaming list once so the merge can run in linear time. */
function indexStreaming(streamingMessages: ChatMessage[]): StreamingIndex {
  const assistants: AssistantEntry[] = []
  const toolCallIds = new Set<string>()
  const toolByCallId = new Map<string, ChatMessage>()
  const optimisticUsers: Array<{ msg: ChatMessage; used: boolean }> = []

  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
      // Empty completed placeholders (no content, no reasoning) are not
      // matchable at all: they have no identity to pair on. toChatMessages also
      // skips the content-less tool-call iteration when building history, so
      // such an entry has no persisted twin and would only ever be appended as
      // a junk bubble. Real (unfinished) iterations keep `streaming: true`.
      const isEmptyPlaceholder =
        msg.streaming !== true && msg.content.trim() === '' && !msg.reasoningContent
      if (isEmptyPlaceholder) continue
      assistants.push({ msg, isStreaming: msg.streaming === true, used: false })
    } else if (msg.role === 'tool') {
      if (msg.toolCallId) {
        toolCallIds.add(msg.toolCallId)
        toolByCallId.set(msg.toolCallId, msg)
      }
    } else if (msg.role === 'user' && msg.optimistic) {
      optimisticUsers.push({ msg, used: false })
    }
  }

  return { assistants, toolCallIds, toolByCallId, optimisticUsers }
}

/**
 * Whether the persisted copy `candidate` already represents `content`.
 *
 * Same 200-char prefix tolerance the confirmation checks use: a turn can end
 * between two renders, so the streaming copy may be a prefix of the persisted
 * one. An empty content never matches (an empty placeholder is not a copy).
 */
function contentConfirmed(content: string, candidate: string): boolean {
  const prefix = content.slice(0, 200)
  return prefix.length > 0 && candidate.startsWith(prefix)
}

/**
 * Identity of a streaming/base assistant pair, per the ONE rule in
 * lib/messageIdentity.ts (text, or the reasoning text when there is no text).
 * Empty string means "not identifiable" — see `findStreamingTwin`.
 */
const assistantIdentity = (m: ChatMessage): string =>
  assistantIdentityText(m.content, m.reasoningContent).trim()

/**
 * The streaming entry that IS the base assistant `msg`, or `undefined`.
 *
 * Matching is by CONTENT IDENTITY, never by position. Positional pairing ("the
 * Nth-from-the-end streaming assistant is the Nth-from-the-end base assistant")
 * is unfixable in the real event stream, because two independent conditions
 * break its count invariant and each one shifts the pairing by a slot:
 *
 *  1. `handleHistoryUpdated` strips live copies only once the async refetch
 *     lands, so a snapshot can still hold the PREVIOUS turn's live copies while
 *     base already contains them;
 *  2. the backend persists every LLM iteration as its own assistant message, so
 *     a running tool turn whose first iteration already finished (`streaming:
 *     false`) contributes a COMPLETED streaming assistant that base does not
 *     have yet.
 *
 * With one extra completed copy, `a1` was paired with `t2`, `t2` with `a2` and
 * `a2` with the new `t3`, transferring each one's `stableId`: the bubbles swapped
 * React keys and the list re-ordered itself mid-session (production repro with
 * lib/orderingMonitor.ts reported `stable-order` from turn 3 on).
 *
 * Two tiers, both confined to COMPLETED (`streaming !== true`) unused entries —
 * an actively-streaming assistant is a new message with no base counterpart yet,
 * so pairing it here would displace its own base copy:
 *
 *   - exact: byte-identical identity. Unambiguous and order-independent, so it
 *     is tried for every base assistant first; this is what pairs the persisted
 *     copy with its live twin in the normal (already-persisted) case.
 *   - prefix: the live copy is a PREFIX of the persisted one (a turn can end
 *     between two renders, and the persisted text kept growing). Only accepted
 *     when the live identity is NOT already rendered from some other base
 *     assistant: that shape is a stale leftover of that other message, and
 *     letting it donate its React key is precisely the key-stealing defect.
 */
function findStreamingTwin(
  msg: ChatMessage,
  index: StreamingIndex,
  baseIdentities: Set<string>,
): AssistantEntry | undefined {
  const identity = assistantIdentity(msg)
  if (identity === '') return undefined
  const candidates = index.assistants.filter((e) => !e.used && !e.isStreaming)

  const exact = candidates.find((e) => assistantIdentity(e.msg) === identity)
  if (exact) return exact

  return candidates.find((e) => {
    const liveIdentity = assistantIdentity(e.msg)
    if (liveIdentity === '' || baseIdentities.has(liveIdentity)) return false
    return contentConfirmed(liveIdentity, identity)
  })
}

/**
 * Whether the persisted assistant `candidate` already represents `msg`, i.e.
 * the streaming copy is a prefix of (or identical to) the persisted text.
 *
 * Goes through the shared identity rule (lib/messageIdentity.ts) so a thinking
 * step or the tool-call iteration of a model that emits no text
 * (`content === ''`, payload in `reasoningContent`) is recognised: keying those
 * on `content` alone made them unmatchable, and with the 50-message sliding
 * history window of pkg/channels/rest_chat.go they accumulated as duplicates at
 * the end of the list until a page reload cleared the streaming state.
 */
function assistantAlreadyPersisted(msg: ChatMessage, candidate: ChatMessage): boolean {
  const identity = assistantIdentity(msg)
  if (identity === '') return false
  return contentConfirmed(identity, assistantIdentity(candidate))
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
function buildFilteredBase(baseMessages: ChatMessage[], index: StreamingIndex): BasePassResult {
  const consumedToolIds = new Set<string>()
  const filteredBase: ChatMessage[] = []
  let optimisticUserIdx = 0
  // Identities already present in base. `findStreamingTwin` needs the whole set
  // up front (not just the messages walked so far) to tell a stale duplicate of
  // a LATER base message from a fresh live copy.
  const baseIdentities = new Set(
    baseMessages.filter((m) => m.role === 'assistant').map(assistantIdentity),
  )

  for (const msg of baseMessages) {
    if (msg.role === 'assistant') {
      // Only the React key is transferred, never the content: the persisted copy
      // stays authoritative and keeps its slot in the server order.
      const entry = findStreamingTwin(msg, index, baseIdentities)
      if (!entry) {
        filteredBase.push(msg)
        continue
      }
      entry.used = true
      filteredBase.push({ ...msg, stableId: entry.msg.stableId ?? entry.msg.id })
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
        // Keep the live streaming copy (same React key as what's on screen)
        // so the card does not remount when history catches up.
        filteredBase.push({
          ...streamingTool,
          stableId: streamingTool.stableId ?? streamingTool.id,
        })
        consumedToolIds.add(msg.toolCallId)
      } else {
        filteredBase.push(msg)
      }
      continue
    }

    // NOTE: do NOT drop base tools that lack a toolCallId just because a
    // streaming tool exists for the session. That rule wiped every historical
    // tool card as soon as a new tool started executing — the "tool calls
    // disappear" bug. Unmatched base tools stay put; the streaming copy is
    // appended as a leftover only when it is not confirmed in base.

    filteredBase.push(msg)
  }

  // Duplicate sweep: a COMPLETED streaming assistant that `findStreamingTwin`
  // could not pair (its base twin was already claimed by an earlier copy) but
  // whose content IS already rendered from base is a stale leftover of an
  // earlier turn. Mark it used so filterStreamingLeftovers drops it instead of
  // appending a second copy — WITHOUT transferring its stableId, which is what
  // let it steal an unrelated recent base assistant's React key.
  for (const e of index.assistants) {
    if (e.used || e.isStreaming) continue
    // Same identity rule as the pairing above (lib/messageIdentity.ts): a
    // thinking / tool-call iteration
    // has no text but IS persisted, so it must be swept by its reasoning text
    // too — otherwise it is appended as a duplicate of the base copy.
    if (assistantIdentityText(e.msg.content, e.msg.reasoningContent).trim() === '') continue
    const alreadyRendered = baseMessages.some(
      (bm) => bm.role === 'assistant' && assistantAlreadyPersisted(e.msg, bm),
    )
    if (alreadyRendered) e.used = true
  }

  return { filteredBase, consumedToolIds }
}

// ── Attachments-only identity ───────────────────────────────────────────────

/**
 * Header the backend writes for a turn that carries attachments but no text:
 * `RenderUserMessage("") -> utils.BuildAttachmentContext` produces
 * "## Attachments\n- <path>" (pkg/agent/context.go, pkg/utils/attachments.go),
 * and that RENDERED text is what the session history stores.
 *
 * WHY the marker and not the path: the persisted path is chosen by the server —
 * `PersistAttachmentsToWorkspace` moves the upload to
 * `<workspace>/attachments/<YYYYMMDD>/` under a fresh `<uuid8>_<name>` — so the
 * client can never predict it. The marker is the only stable part of the
 * persisted copy.
 */
export const ATTACHMENTS_ONLY_MARKER = '## Attachments'

/**
 * Whether `msg` is an attachments-only user turn: no text at all, but at least
 * one attachment. That is a valid send (the composer and the backend both accept
 * it), and its confirmation identity cannot be its content — an empty prefix
 * matches nothing.
 */
export function isAttachmentsOnly(msg: ChatMessage): boolean {
  return msg.role === 'user' && msg.content.trim() === '' && (msg.attachments?.length ?? 0) > 0
}

/**
 * Whether a CONFIRMED (non-optimistic) base user can vouch for an
 * attachments-only optimistic bubble.
 *
 * Two shapes of the SAME persisted turn reach this function:
 *   - raw history entries, whose content still carries the backend marker;
 *   - entries already passed through `toChatMessages`, where
 *     `parseAttachmentsFromContent` MOVED the marker into `attachments` and left
 *     `content` empty. This is the shape the app really sees: `useChatHistory`
 *     caches `toChatMessages(history.messages)` as `baseMessages`, and
 *     `isConfirmedInCache` reads that same cache.
 * Both are recognised, and neither depends on the persisted path.
 */
export function isAttachmentsOnlyBase(bm: ChatMessage): boolean {
  if (bm.role !== 'user' || bm.optimistic) return false
  if ((bm.attachments?.length ?? 0) > 0) return bm.content.trim() === ''
  return bm.content.startsWith(ATTACHMENTS_ONLY_MARKER)
}

// ── Optimistic user confirmation ────────────────────────────────────────────

/**
 * Indexes of the confirmed base users that can vouch for optimistic user
 * `msg`: content matches for its 200-char prefix that no EARLIER optimistic
 * message has claimed yet.
 *
 * An attachments-only turn has an empty prefix, and this used to yield no
 * candidates at all ("an empty placeholder is not a copy"). That made the bubble
 * IMMORTAL the moment the composer and the backend started accepting
 * attachments-only sends: it was never confirmed, and once the history copy
 * landed the image rendered TWICE (staging path + persisted path). Its identity
 * is therefore the marker, not the content. An empty bubble with no attachment
 * is still not a copy and still matches nothing.
 */
function collectConfirmCandidates(
  msg: ChatMessage,
  baseMessages: ChatMessage[],
  claimed: Set<number>,
): number[] {
  const prefix = msg.content.slice(0, 200)
  const candidates: number[] = []
  if (prefix.length === 0) {
    if (!isAttachmentsOnly(msg)) return []
    for (let i = 0; i < baseMessages.length; i++) {
      if (isAttachmentsOnlyBase(baseMessages[i]) && !claimed.has(i)) candidates.push(i)
    }
    return candidates
  }
  for (let i = 0; i < baseMessages.length; i++) {
    const bm = baseMessages[i]
    if (bm.role === 'user' && !bm.optimistic && bm.content.startsWith(prefix) && !claimed.has(i)) {
      candidates.push(i)
    }
  }
  return candidates
}

/**
 * Index of the base message that confirms optimistic user `msg`, or -1.
 *
 * `claimed` holds base indexes already consumed by an EARLIER optimistic
 * message (streaming order is chronological, so claims are handed out in send
 * order). Without it, two rapid sends of identical text both confirmed against
 * the first base copy and the second bubble vanished.
 *
 * Two independent signals, OR-ed:
 *  - legacy count rule: the cached confirmed-user count grew past the send-time
 *    snapshot. Valid only while the history cache grows monotonically.
 *  - anchor rule: a confirmed copy of the text exists AFTER the send-time
 *    anchor id, or the anchor id has slid out of the window entirely (in which
 *    case everything still in the window is newer than the anchor). This is the
 *    window-independent signal that fixes the saturated-window bug.
 */
export function findConfirmingBaseUserIndex(
  msg: ChatMessage,
  baseMessages: ChatMessage[],
  baseUserCount: number,
  claimed: Set<number>,
): number {
  // Rule 1: content match on the 200-char prefix, or the marker identity for an
  // attachments-only turn (empty content → no content match, never no copy).
  const candidates = collectConfirmCandidates(msg, baseMessages, claimed)
  if (candidates.length === 0) return -1

  // Rule 2: legacy count rule — the cached user count grew past the snapshot.
  if (baseUserCount > (msg.optimisticBaseCount ?? 0)) return candidates[0]

  // Rule 3: anchor rule — window-size independent.
  const anchorId = msg.optimisticAnchorId
  if (typeof anchorId === 'string' && anchorId.length > 0) {
    const anchorIdx = baseMessages.findIndex((m) => m.id === anchorId)
    if (anchorIdx === -1) {
      // The anchor slid out of the sliding window: everything still in the
      // window postdates the send — confirm against the newest match.
      return candidates[candidates.length - 1]
    }
    const after = candidates.filter((i) => i > anchorIdx)
    if (after.length > 0) return after[0]
  }

  return -1
}

// ── Streaming pass ──────────────────────────────────────────────────────────

/**
 * Filter the streaming list down to messages not yet represented in base.
 *
 * Drops:
 *   - optimistic user messages once base has caught up,
 *   - assistants already matched into filteredBase (`used`),
 *   - empty completed assistants with no reasoning content (placeholders),
 *   - tool messages already confirmed in base.
 *
 * A completed assistant that was NOT position-matched to a base assistant is a
 * NEW message that base hasn't caught up to yet: the backend persists each LLM
 * iteration as a SEPARATE message (it does not merge a multi-iteration tool
 * turn into a single assistant in HTTP history). We must keep it — otherwise
 * the final answer of a tool turn vanishes when it finishes a moment before
 * the HTTP poll lands, because base already holds that turn's first iteration
 * and the old positional matcher consumed the final assistant as if it were an
 * old leftover.
 */
function filterStreamingLeftovers(
  streamingMessages: ChatMessage[],
  index: StreamingIndex,
  baseMessages: ChatMessage[],
  filteredBase: ChatMessage[],
  consumedToolIds: Set<string>,
): ChatMessage[] {
  const baseUserCount = baseMessages.filter((m) => m.role === 'user' && !m.optimistic).length
  const usedAssistantIds = new Set(index.assistants.filter((e) => e.used).map((e) => e.msg.id))

  // Base indexes already consumed by an EARLIER optimistic user. Threaded
  // across the WHOLE pass so claims are handed out in streaming (= send)
  // order: two in-flight sends of identical text can never confirm against
  // the same base copy (the second would otherwise vanish).
  const claimed = new Set<number>()

  const kept = streamingMessages.filter((msg) => {
    if (msg.role === 'user') {
      if (!msg.optimistic) return true
      // Confirm an optimistic user only when BOTH hold:
      //   1. base already contains a non-optimistic user with the same text
      //      (content match — tolerates server normalization via prefix), AND
      //   2. base user count has grown past the snapshot taken at send time.
      //
      // Content-match alone is not enough: the same text can legitimately
      // appear earlier in the conversation. Count alone is not enough either:
      // two rapid sends share the same optimisticBaseCount, so confirming the
      // first used to drop the second (user message vanishes / order breaks).
      //
      // SATURATED-WINDOW BUG (why the anchor exists): rule 2 is only valid
      // while the cached history grows monotonically — it does not. The
      // backend serves history as a sliding window of the last `limit`
      // messages (pkg/channels/rest_chat.go, `resultStartIdx := endIdx - limit`)
      // and useChatHistory's queryFn REPLACES the cache with that window
      // whenever `cachedData.messages.length <= DEFAULT_LIMIT` (DEFAULT_LIMIT
      // = 50). Past 50 messages the cached confirmed-user count SATURATES, so
      // `baseUserCount > optimisticBaseCount` is false forever, the bubble
      // becomes immortal and mergeMessages strands it at the END — after newer
      // answers — until a page refresh clears streamingMessages. The
      // send-time anchor id (`optimisticAnchorId`, a position-independent
      // content-derived base id) confirms against position instead of counts.
      const confirmIdx = findConfirmingBaseUserIndex(msg, baseMessages, baseUserCount, claimed)
      if (confirmIdx !== -1) {
        claimed.add(confirmIdx)
        return false
      }
      return true
    }

    if (msg.role === 'assistant') {
      if (usedAssistantIds.has(msg.id)) return false
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

  return kept
}

// ── Render-key guard ───────────────────────────────────────────────────────

/**
 * Last-resort guard: the render key React/Virtuoso uses is `stableId ?? id`, and
 * a duplicate key makes a virtualized list mis-render or silently drop a bubble
 * — the "messages lose their order until I refresh the page" symptom.
 *
 * Base data never collides: residentMessageID occurrence-indexes repeated content
 * (pkg/channels/rest_chat.go). Collisions can only be introduced by the merge
 * itself, because buildFilteredBase ASSIGNS stableId when it pairs a streaming
 * message onto a base message (defect D3 was such a key steal). Normalizing here
 * keeps a future merge bug cosmetic instead of structural.
 *
 * The FIRST holder of a key keeps it unchanged — that is the bubble already on
 * screen, so preserving its key avoids a remount. A later colliding entry is
 * re-keyed onto its own `id`, or a `__dup<n>` variant when that is taken too.
 * `id` itself is NEVER rewritten: retry and attachment logic key off it.
 *
 * Returns the input array untouched when there is nothing to fix, so the common
 * path does not allocate.
 */
export function enforceUniqueRenderKeys(messages: ChatMessage[]): ChatMessage[] {
  const seen = new Set<string>()
  let collided = false
  const out = messages.map((m) => {
    const key = m.stableId ?? m.id
    if (!seen.has(key)) {
      seen.add(key)
      return m
    }
    collided = true
    let next = m.id
    let n = 1
    while (seen.has(next)) {
      next = `${m.id}__dup${n++}`
    }
    seen.add(next)
    return { ...m, stableId: next }
  })
  return collided ? out : messages
}

// ── Entry point ─────────────────────────────────────────────────────────────

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
  const { filteredBase, consumedToolIds } = buildFilteredBase(baseMessages, index)

  const filteredStreaming = filterStreamingLeftovers(
    streamingMessages,
    index,
    baseMessages,
    filteredBase,
    consumedToolIds,
  )

  // D4 guard on the COMBINED list — a collision can span the two halves (a
  // base message and a streaming leftover can share a render key), so it is
  // normalized only after concatenation, never per half.
  return enforceUniqueRenderKeys([...filteredBase, ...filteredStreaming])
}
