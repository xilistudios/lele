/**
 * Opt-in ordering instrumentation for the WebUI chat list.
 *
 * WHY this exists: "the message order breaks after a while and only a reload
 * fixes it" has been reported and "fixed" several times. Reproducing it in a
 * unit test requires knowing WHICH render broke and WHAT the two inputs looked
 * like at that instant, so this module keeps a browser-side ring buffer of
 * every WS event, every HTTP history snapshot and every merge, and checks the
 * four invariants whose violation IS the bug:
 *
 *   A `stable-order`   the relative order of two messages that both existed in
 *                      the previous render never flips. This is the literal
 *                      user-visible symptom.
 *   B `duplicate-key`  two entries share a render key (`stableId ?? id`). React
 *                      then drops or misplaces a bubble in the virtualized list.
 *   C `base-order`     merge must not reorder canonical history: the base ids
 *                      that survive into the merged list keep their base order.
 *   D `stranded-copy`  a live (WS) copy is rendered as its OWN entry while
 *                      history already holds the same message (same role +
 *                      identity text). That is the "old block below a newer
 *                      answer" shape.
 *
 * It is DISABLED by default and costs one `isOrderingDebugEnabled()` call per
 * merge when off. Enable it with either
 *
 *   localStorage.setItem('lele.orderingDebug', '1')   (persistent, per browser)
 *   ?orderingDebug=1                                  (one URL, e.g. a bug link)
 *
 * and read the collected evidence in the console with
 *
 *   window.__leleOrderingReport()      full report (violations + trace)
 *   window.__leleOrderingReport().print()   same, human-readable
 *
 * Everything stays in memory plus the last report in `localStorage` (so a
 * crash/tab close does not lose it). No network, no PII beyond message text
 * prefixes (already in the DOM).
 */
import { assistantIdentityText } from './messageIdentity'
import type { ChatMessage } from './types'

/** Ring buffer sizes: enough to hold a few turns of chunks without growing. */
const MAX_TRACE = 600
const MAX_VIOLATIONS = 40
/** How much message text a trace entry keeps — enough to identify, not to leak. */
const TEXT_PREVIEW = 80

export type OrderingKind = 'stable-order' | 'duplicate-key' | 'base-order' | 'stranded-copy'

export type OrderingViolation = {
  kind: OrderingKind
  /** Human-readable, self-contained explanation of what flipped. */
  detail: string
  /** Compact render-order fingerprint at the moment it was detected. */
  merged: string[]
  base: string[]
  streaming: string[]
  /** Index into the trace of the merge that produced it. */
  at: number
  timestamp: number
}

type TraceEntry =
  | { kind: 'ws'; t: number; event: string; data: string }
  | { kind: 'http'; t: number; sessionKey: string; count: number; ids: string[] }
  | { kind: 'merge'; t: number; base: number; streaming: number; merged: string[] }

// ── Enablement ──────────────────────────────────────────────────────────────

/**
 * Whether instrumentation is on. Read from `localStorage` first (persistent
 * opt-in), then the URL query (one-shot, great for "reproduce it here" links).
 * Exported because the wiring points use it to skip building payloads at all.
 */
export function isOrderingDebugEnabled(): boolean {
  if (typeof window === 'undefined') return false
  try {
    if (window.localStorage?.getItem('lele.orderingDebug') === '1') return true
  } catch {
    // Private mode / blocked storage: fall through to the URL check.
  }
  try {
    return new URLSearchParams(window.location.search).get('orderingDebug') === '1'
  } catch {
    return false
  }
}

// ── State ───────────────────────────────────────────────────────────────────

let trace: TraceEntry[] = []
let violations: OrderingViolation[] = []
/**
 * Render order of the PREVIOUS merge, as render keys. `null` means "no previous
 * render to compare against" (first merge, monitor reset, session switch).
 */
let previousOrder: string[] | null = null
let mergeCount = 0

/** Reset the whole buffer. Exported for tests and for `window.__leleOrderingReset`. */
export function resetOrderingMonitor(): void {
  trace = []
  violations = []
  previousOrder = null
  mergeCount = 0
}

// ── Fingerprints ────────────────────────────────────────────────────────────

/** The render key React/Virtuoso uses (see `enforceUniqueRenderKeys`). */
function renderKey(msg: ChatMessage): string {
  return msg.stableId ?? msg.id
}

/** Identity text used for duplicate detection (shared rule, see messageIdentity). */
function identityText(msg: ChatMessage): string {
  if (msg.role === 'assistant') return assistantIdentityText(msg.content, msg.reasoningContent)
  if (msg.role === 'tool') return `tool:${msg.toolCallId ?? ''}:${msg.toolResult ?? ''}`
  return msg.content
}

function preview(msg: ChatMessage): string {
  const text = identityText(msg).replace(/\s+/g, ' ').trim().slice(0, TEXT_PREVIEW)
  return `${msg.role}:${renderKey(msg)}:${text}`
}

function fingerprint(messages: ChatMessage[]): string[] {
  return messages.map(preview)
}

function pushTrace(entry: TraceEntry): void {
  trace.push(entry)
  if (trace.length > MAX_TRACE) trace.splice(0, trace.length - MAX_TRACE)
}

function pushViolation(v: OrderingViolation): void {
  violations.push(v)
  if (violations.length > MAX_VIOLATIONS) violations.shift()
  // Persist immediately: the interesting case is a tab that is reloaded before
  // anyone gets to copy the console.
  try {
    window.localStorage?.setItem(
      'lele.orderingReport',
      JSON.stringify({ violations, mergeCount, savedAt: new Date().toISOString() }),
    )
  } catch {
    // Storage full/blocked: the in-memory report is still authoritative.
  }
}

// ── Recording ───────────────────────────────────────────────────────────────

/**
 * Record one WebSocket event. Called from the single dispatch point in
 * `useMessages` so the trace mirrors exactly what the handlers saw, in order.
 */
export function noteWsEvent(event: string, data: unknown): void {
  if (!isOrderingDebugEnabled()) return
  let serialized: string
  try {
    serialized = JSON.stringify(data ?? null)
  } catch {
    serialized = '"[unserializable]"'
  }
  pushTrace({
    kind: 'ws',
    t: Date.now(),
    event,
    data: serialized.length > 400 ? `${serialized.slice(0, 400)}…` : serialized,
  })
}

/**
 * Record one HTTP history snapshot (the newest page). Its ids are what the
 * merge treats as canonical, so a snapshot whose ids disagree between polls is
 * the smoking gun for "the same message keeps a different identity".
 */
export function noteHistorySnapshot(sessionKey: string, messages: ChatMessage[]): void {
  if (!isOrderingDebugEnabled()) return
  pushTrace({
    kind: 'http',
    t: Date.now(),
    sessionKey,
    count: messages.length,
    ids: messages.map((m) => renderKey(m)),
  })
}

// ── Invariant checks ────────────────────────────────────────────────────────

/** Index pairs where two entries share a render key. */
function findDuplicateKeys(messages: ChatMessage[]): string[] {
  const seen = new Map<string, number>()
  const dups: string[] = []
  messages.forEach((msg, i) => {
    const key = renderKey(msg)
    const first = seen.get(key)
    if (first === undefined) {
      seen.set(key, i)
      return
    }
    dups.push(`key ${key} at #${first} and #${i}`)
  })
  return dups
}

/**
 * Base ids that survive into the merged list, in merged order, compared with
 * their order inside base. Any difference means the merge reordered canonical
 * history (the "answer above its question" shape).
 */
function checkBaseOrder(baseMessages: ChatMessage[], merged: ChatMessage[]): string | undefined {
  const baseIds = baseMessages.map((m) => m.id)
  const baseRank = new Map(baseIds.map((id, i) => [id, i]))
  let lastRank = -1
  for (const msg of merged) {
    const rank = baseRank.get(msg.id)
    if (rank === undefined) continue
    if (rank < lastRank) {
      return `base message ${msg.id} (rank ${rank}) rendered after a base message of rank ${lastRank}`
    }
    lastRank = rank
  }
  return undefined
}

/**
 * Live copies that duplicate a message already present in base.
 *
 * Identity is role + identity text, and only identities that HISTORY ALREADY
 * CONTAINS are considered: a message that is legitimately new (still streaming,
 * no base counterpart yet) has a base count of 0 and can never be stranded —
 * flagging those was a false positive that would drown the real signal.
 * Legitimate repeats inside base are allowed too, because the comparison is
 * "more copies than history holds", not "more than one".
 */
function checkStrandedCopies(
  baseMessages: ChatMessage[],
  merged: ChatMessage[],
): string | undefined {
  const baseCounts = new Map<string, number>()
  for (const msg of baseMessages) {
    const key = `${msg.role}\0${identityText(msg).trim()}`
    baseCounts.set(key, (baseCounts.get(key) ?? 0) + 1)
  }
  const mergedCounts = new Map<string, number>()
  for (const msg of merged) {
    const identity = identityText(msg).trim()
    // Empty identities render nothing; `filterStreamingLeftovers` drops them.
    if (identity === '') continue
    const key = `${msg.role}\0${identity}`
    mergedCounts.set(key, (mergedCounts.get(key) ?? 0) + 1)
  }
  for (const [key, mergedCount] of mergedCounts) {
    const baseCount = baseCounts.get(key) ?? 0
    if (baseCount === 0 || mergedCount <= baseCount) continue
    const [role, identity] = key.split('\0')
    return `${role} "${identity.slice(0, TEXT_PREVIEW)}" rendered ${mergedCount}x but history holds it ${baseCount}x (a live copy rendered as its own entry while history already has the message)`
  }
  return undefined
}

/**
 * Relative order of the keys present in BOTH renders. A pair that swapped is
 * the user-visible "the order broke" event: nothing new arrived, yet a message
 * moved.
 *
 * Compared against the previous order only for keys that survived, so a
 * message scrolling out of the 50-message window (or being legitimately
 * confirmed and re-keyed) cannot produce a false positive: if either member of
 * a pair is missing from one side it simply is not compared.
 */
function findOrderFlips(previous: string[], current: string[]): string | undefined {
  const currentIndex = new Map(current.map((key, i) => [key, i]))
  let lastIndex = -1
  let lastKey = ''
  for (const key of previous) {
    const idx = currentIndex.get(key)
    if (idx === undefined) continue
    if (idx < lastIndex) {
      return `"${lastKey}" was before "${key}" and is now after it`
    }
    lastIndex = idx
    lastKey = key
  }
  return undefined
}

// ── Entry point ─────────────────────────────────────────────────────────────

/**
 * Observe one merge result. Returns `merged` UNCHANGED (identity preserved) so
 * the caller can wrap its `useMemo` value with it on a hot path: when the debug
 * flag is off this is a single boolean check plus a return.
 *
 * `baseMessages` and `streamingMessages` are the merge inputs; they are only
 * used to build the evidence payload of a violation.
 */
export function observeMergedOrder(
  merged: ChatMessage[],
  baseMessages: ChatMessage[],
  streamingMessages: ChatMessage[],
): ChatMessage[] {
  if (!isOrderingDebugEnabled()) return merged

  mergeCount++
  const order = merged.map(renderKey)

  const report = (kind: OrderingKind, detail: string) => {
    pushViolation({
      kind,
      detail,
      merged: fingerprint(merged),
      base: fingerprint(baseMessages),
      streaming: fingerprint(streamingMessages),
      at: trace.length,
      timestamp: Date.now(),
    })
    // Loud on purpose: this is a diagnostic build, and a silent violation is
    // exactly what made the previous investigations fail.
    console.error(`[order] ${kind}: ${detail}`, {
      merged: fingerprint(merged),
      base: fingerprint(baseMessages),
      streaming: fingerprint(streamingMessages),
    })
  }

  const duplicates = findDuplicateKeys(merged)
  if (duplicates.length > 0) report('duplicate-key', duplicates.join('; '))

  const reordered = checkBaseOrder(baseMessages, merged)
  if (reordered) report('base-order', reordered)

  const stranded = checkStrandedCopies(baseMessages, merged)
  if (stranded) report('stranded-copy', stranded)

  if (previousOrder) {
    const flip = findOrderFlips(previousOrder, order)
    if (flip) report('stable-order', flip)
  }
  previousOrder = order

  pushTrace({
    kind: 'merge',
    t: Date.now(),
    base: baseMessages.length,
    streaming: streamingMessages.length,
    merged: order,
  })

  return merged
}

/** The whole collected evidence, plus a `print()` helper for the console. */
export type OrderingReport = {
  mergeCount: number
  violations: OrderingViolation[]
  trace: TraceEntry[]
  previousOrder: string[] | null
  print: () => void
}

export function orderingReport(): OrderingReport {
  const report: OrderingReport = {
    mergeCount,
    violations: [...violations],
    trace: [...trace],
    previousOrder,
    print: () => {
      // biome-ignore lint/suspicious/noConsole: diagnostic entry point.
      console.log(
        `[order] ${violations.length} violation(s) over ${mergeCount} merge(s)\n` +
          violations.map((v, i) => `  ${i + 1}. ${v.kind}: ${v.detail}`).join('\n'),
      )
    },
  }
  return report
}

/** Install the console/global entry points (idempotent). */
export function installOrderingMonitorGlobals(target: Window | undefined = window): void {
  if (!target) return
  const w = target as unknown as Record<string, unknown>
  w.__leleOrderingReport = orderingReport
  w.__leleOrderingReset = resetOrderingMonitor
}