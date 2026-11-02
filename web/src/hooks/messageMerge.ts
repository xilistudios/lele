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
import { isHiddenFromChat, parseSystemNotice } from '../lib/chatMessageBuilder'
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

/** No claims yet: used to ask "which rows COULD a content rule pair with". */
const EMPTY_CLAIMS: ReadonlySet<number> = new Set<number>()

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
  claimed: ReadonlySet<number>,
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
  claimed: ReadonlySet<number>,
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

/**
 * Whether any of the base rows in `rows` POSTDATES the send position of `msg`,
 * i.e. whether history has provably moved past that bubble's turn.
 *
 * WHY this is not just `rows.size > 0`: `rows` are the base indexes that a
 * LATER optimistic bubble can match BY CONTENT, and a content match ignores
 * position — sending the same short text twice ("ok", "continue", "sí") makes
 * the later bubble "reserve" the OLD row that carries that text. Reading that
 * as "history already moved past me" would drop a bubble that is legitimately
 * still in flight (it re-appears from base on the next poll, but the user sees
 * it vanish for up to four seconds). Only a row at/after the send position
 * proves the turn was answered without ever being persisted — which is the
 * built-in slash-command case (`/model`, `/status`, `/compact` are executed by
 * the command handler and only their RESPONSE is delivered).
 *
 * The position test is the same one the confirmation ladder uses: the send-time
 * ordinal when the cache has grown past the snapshot, and the anchor id
 * otherwise (an anchor that slid out of the window means every row still in
 * base postdates the send).
 */
function hasRowPastSendPosition(
  msg: ChatMessage,
  baseMessages: ChatMessage[],
  rows: ReadonlySet<number>,
): boolean {
  if (rows.size === 0) return false

  const snapshot = msg.optimisticBaseCount ?? 0
  const anchorId = msg.optimisticAnchorId
  const hasAnchor = typeof anchorId === 'string' && anchorId.length > 0
  const anchorIdx = hasAnchor ? baseMessages.findIndex((m) => m.id === anchorId) : -1
  // Anchor gone from the sliding window: everything still in base postdates it.
  if (hasAnchor && anchorIdx === -1) return true

  let ordinal = 0
  for (let i = 0; i < baseMessages.length; i++) {
    const bm = baseMessages[i]
    if (bm.role !== 'user' || bm.optimistic) continue
    const thisOrdinal = ordinal++
    if (!rows.has(i)) continue
    if (thisOrdinal >= snapshot) return true
    if (anchorIdx !== -1 && i > anchorIdx) return true
  }
  return false
}

/**
 * Index of the confirmed base user that a STRANDED optimistic bubble `msg`
 * should be reconciled against — the POSITIONAL fallback — or -1.
 *
 * WHY a positional fallback is legitimate for USER messages only: a send and
 * its persisted copy are 1:1 and strictly ordered inside the history window.
 * One sent turn produces exactly one user row (RenderUserMessage,
 * pkg/agent/context.go), so "the first confirmed user at/after my send
 * position" is a well-defined pairing. Assistants are NOT like that: the
 * backend persists ONE MESSAGE PER LLM ITERATION, so a single turn can add N
 * assistant rows and the Nth-from-the-end live copy is not the Nth-from-the-end
 * base copy — see `findStreamingTwin`, which forbids positional pairing for
 * exactly that reason. Never extend this helper to assistant or tool matching.
 *
 * WHY it is needed at all: this is only reachable AFTER every content-based
 * rule of `findConfirmingBaseUserIndex` failed, i.e. `collectConfirmCandidates`
 * returned `[]`. That happens when the server persisted the turn but under a
 * DIFFERENT text than the client sent — server-side normalization (trimming,
 * the rendered attachment context, rewrites, system notices) defeats the
 * 200-char prefix identity — and, independently, when the client's
 * content-derived anchor id has already slid out of the sliding history window
 * (pkg/channels/rest_chat.go caps history at the last `limit` messages). In
 * both shapes no rule can ever confirm the bubble, and `mergeMessages` appends
 * unmatched streaming messages after canonical history: it renders BELOW newer
 * answers forever ("an old user message shows up as the newest") until a page
 * reload clears the streaming state.
 *
 * ACCEPTED TRADE-OFF: with two rapid sends and only ONE persisted copy, the
 * FIRST bubble claims the persisted slot (render A, pinned by test 5) and the
 * second is left unmatched — it keeps rendering at the tail until its own copy
 * lands, exactly as before this fallback existed. Preferring the older bubble is
 * deliberate: it is the one whose copy the server writes first, and a claim
 * consumed out of order would strand the newer send instead.
 *
 * Both rules hand out only indexes that are unclaimed and NOT optimistic, so a
 * claim can be consumed exactly once across a whole merge pass.
 *
 * TWO further exclusions keep the rule from pairing the WRONG row, and both are
 * required for correctness, not polish:
 *
 *  - `reserved` — every base index that ANY optimistic bubble of this pass can
 *    still match by CONTENT. A positional claim must never take one of them,
 *    because claims are handed out in send order: an older bubble the server
 *    never persisted (a built-in slash command such as `/model` or `/status` is
 *    executed and answered WITHOUT ever being written to the session, so its
 *    bubble can never be confirmed) would otherwise claim the row belonging to
 *    the NEXT send, and that later bubble — whose copy is right there in base —
 *    would then have nothing left to pair with. It would be appended AFTER the
 *    canonical history and render below newer answers forever, which is exactly
 *    the ordering symptom this fallback exists to remove.
 *  - `isHiddenFromChat` — a row the chat log never renders (compaction summary,
 *    loop-detector guidance). Pairing with one deletes the user's bubble in
 *    exchange for nothing visible, so the message would simply vanish.
 *  - a synthetic `[System: …]` notice — persisted with role "user" but written
 *    by the BACKEND, so it can never be the copy of a typed message.
 */
export function findUnclaimedBaseUserForStaleBubble(
  msg: ChatMessage,
  baseMessages: ChatMessage[],
  baseUserCount: number,
  claimed: ReadonlySet<number>,
  reserved?: ReadonlySet<number>,
): number {
  // ATTACHMENTS-CARRYING turns are excluded: their identity is the attachment
  // marker, not their text (the persisted path is chosen by the server —
  // `PersistAttachmentsToWorkspace` renames the upload — see
  // ATTACHMENTS_ONLY_MARKER / isAttachmentsOnlyBase). Pairing them by position
  // would let an unrelated turn absorb the bubble, which
  // attachmentsOnlySend.test.ts (5, 5b, 5c) pins as forbidden. A text+attachment
  // turn is already covered by the content rules anyway: the server persists it
  // as `<text>\n\n<attachment context>` (RenderUserMessage, pkg/agent/context.go),
  // so the client text stays a prefix of the persisted copy.
  if ((msg.attachments?.length ?? 0) > 0) return -1

  /** A base row this bubble is allowed to consume as its persisted copy. */
  const claimable = (i: number): boolean => {
    if (claimed.has(i)) return false
    if (reserved?.has(i)) return false
    const bm = baseMessages[i]
    if (bm.role !== 'user' || bm.optimistic) return false
    if (isHiddenFromChat(bm)) return false
    // A synthetic system notice (`[System: <sender>] …`) is persisted with role
    // "user" but the BACKEND writes it; it is never the copy of something the
    // user typed ([System: is the canonical synthetic marker, see
    // parseSystemNotice). Claiming one would delete the user's bubble and leave
    // only the notice on screen.
    if (parseSystemNotice(bm.content)) return false
    return true
  }

  const snapshot = msg.optimisticBaseCount ?? 0

  // Rule 1 — send-time ordinal. Covers the FIRST message of a session, where
  // `optimisticAnchorId` is undefined because the history cache was still
  // empty at send time: the snapshot is then just a count, and every confirmed
  // user whose 0-based ordinal among confirmed users is >= that count arrived
  // AFTER the send. The FIRST such unclaimed index is this bubble's slot.
  if (baseUserCount > snapshot) {
    let ordinal = 0
    for (let i = 0; i < baseMessages.length; i++) {
      const bm = baseMessages[i]
      if (bm.role !== 'user' || bm.optimistic) continue
      const thisOrdinal = ordinal++
      if (thisOrdinal < snapshot) continue
      if (claimable(i)) return i
    }
  }

  // Rule 2 — anchor id. Base ids are content-derived and position-independent
  // (residentMessageID, pkg/channels/rest_chat.go), so the anchor survives
  // history rewrites; only the sliding window can remove it.
  const anchorId = msg.optimisticAnchorId
  if (typeof anchorId !== 'string' || anchorId.length === 0) return -1

  const confirmedUsers: number[] = []
  for (let i = 0; i < baseMessages.length; i++) {
    const bm = baseMessages[i]
    if (bm.role === 'user' && !bm.optimistic) confirmedUsers.push(i)
  }
  const unclaimed = confirmedUsers.filter(claimable)

  const anchorIdx = baseMessages.findIndex((m) => m.id === anchorId)
  if (anchorIdx === -1) {
    // The anchor slid out of the window: every confirmed user still in base
    // postdates the send, so this bubble's copy is the NEWEST one.
    return unclaimed.length > 0 ? unclaimed[unclaimed.length - 1] : -1
  }
  // Anchor still visible: the copy is the first confirmed user after it.
  for (const i of unclaimed) {
    if (i > anchorIdx) return i
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

  // The optimistic user bubbles, in send order, with the base rows each one can
  // match BY CONTENT (ignoring claims: this is "which rows can a content rule
  // ever pair with", not "which claim it takes").
  const bubbles = streamingMessages
    .filter((m) => m.role === 'user' && m.optimistic)
    .map((msg) => ({ msg, candidates: collectConfirmCandidates(msg, baseMessages, EMPTY_CLAIMS) }))

  // Base rows that a CONTENT rule will pair with some optimistic bubble that
  // comes AFTER this one in send order. Two uses, both required:
  //
  //  1. the positional fallback must treat them as off limits (they belong to a
  //     later, properly-persisted send — see the doc comment above);
  //  2. if there is one at all, base has already moved PAST this bubble's turn,
  //     so a copy of this bubble is never going to appear: the caller drops the
  //     bubble WITHOUT consuming a claim.
  const suffixReserved: ReadonlySet<number>[] = new Array<ReadonlySet<number>>(bubbles.length)
  const running: Set<number> = new Set<number>()
  for (let i = bubbles.length - 1; i >= 0; i--) {
    suffixReserved[i] = new Set(running)
    for (const idx of bubbles[i].candidates) running.add(idx)
  }
  const bubblePositions = new Map<string, number>()
  bubbles.forEach((b, i) => bubblePositions.set(b.msg.id, i))

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
      // A refused/failed send must stay visible AND retryable: it never got a
      // persisted copy, so no fallback below may swallow it.
      if (msg.failed) return true
      // FINAL FALLBACK (positional): the server persisted this turn but under a
      // different text, or the anchor id slid out of the window — no content
      // rule can ever confirm the bubble, and keeping it would strand it BELOW
      // newer answers. See findUnclaimedBaseUserForStaleBubble.
      const bubblePos = bubblePositions.get(msg.id) ?? -1
      const futureReserved = bubblePos >= 0 ? suffixReserved[bubblePos] : EMPTY_CLAIMS
      const staleIdx = findUnclaimedBaseUserForStaleBubble(
        msg,
        baseMessages,
        baseUserCount,
        claimed,
        futureReserved,
      )
      if (staleIdx !== -1) {
        claimed.add(staleIdx)
        return false
      }
      // No row to claim, but a LATER send already has one by content: base has
      // moved past this bubble's turn, so its copy is not coming — the server
      // answered it without ever writing it to the session (a built-in slash
      // command: `/model`, `/status`, `/compact`, … are executed by the command
      // handler and only their RESPONSE is delivered) or its row has slid out of
      // the sliding window. Drop the bubble WITHOUT consuming a claim, so the
      // later send still finds the row it owns. This is what stops a command
      // bubble from being the immortal "old message rendered as the newest".
      // …but only a row that POSTDATES this bubble's send position proves it.
      // A later send whose text merely repeats OLDER text also reserves a row
      // (the content candidates ignore position), and treating that as "my
      // copy is not coming" would drop a bubble that is legitimately still in
      // flight — it would vanish for up to one poll and then re-appear from
      // base. See hasRowPastSendPosition.
      if (hasRowPastSendPosition(msg, baseMessages, futureReserved)) return false
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
 *
 * Optimistic USER bubbles are confirmed by content first (prefix identity, or
 * the attachments-only marker) with the count/anchor novelty rules on top of
 * it, and then — for a turn the server persisted under a DIFFERENT text, or
 * whose anchor slid out of the window — by a FINAL POSITIONAL fallback
 * (`findUnclaimedBaseUserForStaleBubble`), which is sound for user turns only.
 * Without it such a bubble could never be confirmed and would be appended
 * below newer answers forever. A bubble that can claim nothing while a LATER
 * send already matched a row by content is dropped without consuming a claim:
 * that is the built-in-slash-command case (`/model`, `/status`, … are answered
 * without ever being persisted), which would otherwise leave one immortal
 * phantom per command.
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
