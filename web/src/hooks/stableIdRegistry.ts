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
 */
export function lookupStableId(role: string, content: string, occurrence = 0): string | undefined {
  return registry.get(stableIdKey(role, content))?.[occurrence]
}

/** Test-only: clear the registry between tests. */
export function clearStableIdRegistry(): void {
  registry.clear()
}
