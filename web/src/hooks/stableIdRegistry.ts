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
 * Memory safety: entries are keyed by content prefix and capped. Old entries
 * are harmless (a stale entry only re-attaches a stableId to a message with
 * identical role+content, which is exactly the desired behavior).
 */

const MAX_ENTRIES = 500
const PREFIX_LEN = 200

const registry = new Map<string, string>()

function registryKey(role: string, content: string): string {
  return `${role}:${content.slice(0, PREFIX_LEN)}`
}

/**
 * Record that a streaming message with `ephemeralId` was confirmed into the
 * HTTP history as a message with `role` and `content`. Later history builds
 * will attach `ephemeralId` as `stableId` to the canonical copy.
 */
export function registerStableId(role: string, content: string, ephemeralId: string): void {
  if (!ephemeralId || !content) return
  const key = registryKey(role, content)
  // Re-insert to refresh LRU recency (Map preserves insertion order).
  registry.delete(key)
  registry.set(key, ephemeralId)
  if (registry.size > MAX_ENTRIES) {
    const oldest = registry.keys().next().value
    if (oldest !== undefined) registry.delete(oldest)
  }
}

/**
 * Look up the stable id recorded for a message with the given role/content,
 * or undefined if none was recorded.
 */
export function lookupStableId(role: string, content: string): string | undefined {
  return registry.get(registryKey(role, content))
}

/** Test-only: clear the registry between tests. */
export function clearStableIdRegistry(): void {
  registry.clear()
}
