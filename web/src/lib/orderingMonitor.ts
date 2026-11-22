import { assistantIdentityText } from './messageIdentity'
import type { ChatMessage } from './types'

const MAX_TRACE = 600
const MAX_VIOLATIONS = 40
const MAX_KEYS = 4000
const TEXT_PREVIEW = 80

export type OrderingKind =
  | 'stable-order'
  | 'duplicate-key'
  | 'base-order'
  | 'stranded-copy'
  | 'key-identity'

export type OrderingViolation = {
  kind: OrderingKind
  detail: string
  merged: string[]
  base: string[]
  streaming: string[]
  at: number
  timestamp: number
}

type TraceEntry =
  | { kind: 'ws'; t: number; event: string; data: string }
  | { kind: 'http'; t: number; sessionKey: string; count: number; ids: string[] }
  | { kind: 'merge'; t: number; base: number; streaming: number; merged: string[] }

export function isOrderingDebugEnabled(): boolean {
  if (typeof window === 'undefined') return false
  try {
    if (window.localStorage?.getItem('lele.orderingDebug') === '1') return true
  } catch {}
  try {
    return new URLSearchParams(window.location.search).get('orderingDebug') === '1'
  } catch {
    return false
  }
}

let trace: TraceEntry[] = []
let violations: OrderingViolation[] = []
let previousOrder: string[] | null = null
const keyIdentities = new Map<string, string>()
let mergeCount = 0

export function resetOrderingMonitor(): void {
  trace = []
  violations = []
  previousOrder = null
  keyIdentities.clear()
  mergeCount = 0
}

function renderKey(msg: ChatMessage): string {
  return msg.stableId ?? msg.id
}

function identityText(msg: ChatMessage): string {
  if (msg.role === 'assistant') return assistantIdentityText(msg.content, msg.reasoningContent)
  if (msg.role === 'tool') return `tool:${msg.toolCallId ?? ''}:${msg.toolResult ?? ''}`
  return msg.content
}

function preview(msg: ChatMessage): string {
  const text = identityText(msg).replace(/\s+/g, ' ').trim().slice(0, TEXT_PREVIEW)
  return JSON.stringify([msg.role, renderKey(msg), text])
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
  try {
    window.localStorage?.setItem(
      'lele.orderingReport',
      JSON.stringify({ violations, mergeCount, savedAt: new Date().toISOString() }),
    )
  } catch {}
}

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
    if (msg.streaming === true) continue
    const identity = identityText(msg).trim()
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

function stableIdentityText(msg: ChatMessage): string {
  if (msg.role === 'tool' && msg.toolCallId) return `tool:${msg.toolCallId}`
  return identityText(msg).trim()
}

function findKeyRepointing(messages: ChatMessage[]): string | undefined {
  let firstMismatch: string | undefined
  for (const msg of messages) {
    if (msg.streaming === true) continue
    const key = renderKey(msg)
    const text = stableIdentityText(msg)
    if (text === '') continue
    const previous = keyIdentities.get(key)
    if (previous !== undefined && previous !== text && firstMismatch === undefined) {
      firstMismatch = `key ${key} used to be "${previous.slice(0, TEXT_PREVIEW)}" and now renders "${text.slice(0, TEXT_PREVIEW)}"`
    }
    keyIdentities.delete(key)
    keyIdentities.set(key, text)
    if (keyIdentities.size > MAX_KEYS) {
      const oldest = keyIdentities.keys().next().value
      if (oldest !== undefined) keyIdentities.delete(oldest)
    }
  }
  return firstMismatch
}

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
    console.error(`[order] ${kind}: ${detail}`, {
      merged: fingerprint(merged),
      base: fingerprint(baseMessages),
      streaming: fingerprint(streamingMessages),
    })
  }

  const duplicates = findDuplicateKeys(merged)
  if (duplicates.length > 0) report('duplicate-key', duplicates.join('; '))

  const repointed = findKeyRepointing(merged)
  if (repointed) report('key-identity', repointed)

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
      const lines = violations.map((v, i) => `  ${i + 1}. ${v.kind}: ${v.detail}`).join('\n')
      // biome-ignore lint/suspicious/noConsole: diagnostic entry point.
      console.log(`[order] ${violations.length} violation(s) over ${mergeCount} merge(s)\n${lines}`)
    },
  }
  return report
}

export function installOrderingMonitorGlobals(target: Window | undefined = window): void {
  if (!target) return
  const w = target as unknown as Record<string, unknown>
  w.__leleOrderingReport = orderingReport
  w.__leleOrderingReset = resetOrderingMonitor
}
