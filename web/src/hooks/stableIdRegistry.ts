/**
 * Durable stableId registry.
 *
 * Problem this solves:
 *
 * When a streaming message (ephemeral UUID id) is confirmed into the HTTP
 * history cache, its canonical copy gets a content-hash id. If the React
 * render key changes from the uuid to the hash at that moment, the bubble
 * remounts and replays its enter animation — visible flicker.
 *
 * `mergeMessages` already carries the ephemeral id forward as `stableId`
 * when BOTH copies coexist, but `handleHistoryUpdated` strips the streaming
 * copy as soon as the cache holds the confirmed one — so the two copies
 * never coexist and the carry-over never happens.
 *
 * A one-off patch of the react-query cache is NOT durable: every refetch
 * (including the 4s processing poll) rebuilds the cache via `toChatMessages`
 * and would wipe the patch, deferring the flicker.
 *
 * This registry provides the durable layer: `handleHistoryUpdated` records
 * the mapping (role + content prefix → ephemeral id) at confirmation time,
 * and `toChatMessages` consults it on EVERY history build, re-attaching the
 * ephemeral id as `stableId`. React keys therefore stay identical across
 * the WebSocket→HTTP transition AND across all subsequent refetches.
 *
 * ## Why occurrence indexing matters
 *
 * The history message id is derived from CONTENT (sha256 of role|content),
 * so two genuinely different messages with identical content share the same
 * id. The registry key is also content-based (`role:content_prefix`), so
 * those two messages map to the SAME registry key.
 *
 * Without occurrence indexing, the second registration overwrites the first,
 * both messages look up the same ephemeral id, and React sees duplicate keys
 * — dropping one bubble.
 *
 * The registry now stores an *array* of ephemeral ids per key (one per
 * confirmed copy). `lookupStableId` accepts an occurrence index so the Nth
 * message with identical content retrieves the Nth recorded id.
 *
 * Memory safety: entries are keyed by content prefix and capped. Old entries
 * are harmless (a stale entry only re-attaches a stableId to a message with
 * identical role+content, which is exactly the desired behavior).
 */

const MAX_ENTRIES = 500
const PREFIX_LEN = 200

const registry = new Map<string, string[]>()

/** Registry key for a role/content pair. Exported so call sites can count
 *  occurrences using the exact same truncation. */
export function stableIdKey(role: string, content: string): string {
  return `${role}:${content.slice(0, PREFIX_LEN)}`
}

/**
 * Record that a streaming message with `ephemeralId` was confirmed into the
 * HTTP history as a message with `role` and `content`. Later history builds
 * will attach `ephemeralId` as `stableId` to the canonical copy.
 *
 * Duplicate ephemeral ids (the same streaming message confirmed multiple
 * times) are silently ignored so that the occurrence count stays correct.
 */
export function registerStableId(role: string, content: string, ephemeralId: string): void {
  if (!ephemeralId || !content) return
  const key = stableIdKey(role, content)
  let ids = registry.get(key)
  if (!ids) {
    ids = []
    registry.set(key, ids)
  }
  if (!ids.includes(ephemeralId)) {
    ids.push(ephemeralId)
  }
  // Re-insert to refresh LRU recency (Map preserves insertion order).
  registry.delete(key)
  registry.set(key, ids)
  if (registry.size > MAX_ENTRIES) {
    const oldest = registry.keys().next().value
    if (oldest !== undefined) registry.delete(oldest)
  }
}

/**
 * Look up the stable id recorded for a message with the given role/content
 * at the given occurrence index, or undefined if none was recorded.
 *
 * The default occurrence (0) covers the common case of a single message with
 * unique content. When two messages share identical content, the caller must
 * pass the occurrence index (0 for the first, 1 for the second, etc.).
 *
 * Historical consumer: `toChatMessages` used to call this with an occurrence
 * index counted INSIDE the fetched history page, which is not a stable place
 * to count from (see `lookupStableIdFromEnd`). Prefer that one for messages
 * that come out of the sliding history window.
 */
export function lookupStableId(role: string, content: string, occurrence = 0): string | undefined {
  return registry.get(stableIdKey(role, content))?.[occurrence]
}

/**
 * Look up the stable id recorded for the `fromEnd`-th NEWEST message with this
 * role/content (0 = the most recently registered copy), or undefined.
 *
 * WHY the newest end is the only stable end to count from: the backend serves
 * history as a sliding window of the last `limit` messages
 * (pkg/channels/rest_chat.go), so the OLDEST messages fall off the page while
 * the registry keeps one entry per copy ever confirmed by this page. Counting
 * occurrences from the START of the page (the old `lookupStableId` call) makes
 * copy #N of an identity point at the N-th registered id — and as soon as one
 * older copy slides out, EVERY later copy reads its predecessor's id.
 *
 * Consequence in the browser (reproduced against a live gateway): the React
 * key of a bubble moves to a different message. React/Virtuoso then reconcile
 * the wrong DOM node into the slot (bubbles swap places, an old message shows
 * up where the newest one belongs) and the transcript looks reordered until a
 * reload clears the registry. Tools made it systematic: their identity is the
 * tool RESULT (`content`), which is often identical across calls (`git status`,
 * `ls`, a failing test), and every thinking model repeats reasoning verbatim.
 *
 * Counting from the newest end is stable under exactly the mutation that
 * happens in practice (old copies leaving the window) and never binds an id to
 * a copy that is not in the page (an out-of-range `fromEnd` is `undefined`, so
 * the caller falls back to the message's own id).
 *
 * The rank is still only as good as the CALLER's knowledge of the page, which
 * is why `toChatMessages` gates this on two conditions (see `bindStableIds`):
 * the page must be the TAIL of the session, and every copy of the identity in
 * it must be confirmed (`countStableIds >= copies in page`). Without that gate
 * an OLDER page (loadMore) would hand its copies the ids of newer messages
 * that are still on screen.
 */
export function lookupStableIdFromEnd(
  role: string,
  content: string,
  fromEnd = 0,
): string | undefined {
  const ids = registry.get(stableIdKey(role, content))
  if (!ids || ids.length === 0) return undefined
  const index = ids.length - 1 - fromEnd
  return index >= 0 ? ids[index] : undefined
}

/**
 * How many copies of this identity the registry holds an id for.
 *
 * A caller that wants to align a page's copies with the recorded ids needs
 * this to know whether the alignment can be exact at all: a page may hold
 * copies that were never confirmed here (older pages, or a refetch that won
 * the race against the `history.updated` event), and binding those by rank is
 * what moves a React key onto another message.
 */
export function countStableIds(role: string, content: string): number {
  return registry.get(stableIdKey(role, content))?.length ?? 0
}

/** Test-only: clear the registry between tests. */
export function clearStableIdRegistry(): void {
  registry.clear()
}
