import { isHiddenFromChat, parseSystemNotice } from '../lib/chatMessageBuilder'
import { assistantIdentityText } from '../lib/messageIdentity'
import type { ChatMessage } from '../lib/types'

type AssistantEntry = {
  msg: ChatMessage
  isStreaming: boolean
  used: boolean
}

type StreamingIndex = {
  assistants: AssistantEntry[]
  toolCallIds: Set<string>
  toolByCallId: Map<string, ChatMessage>
  optimisticUsers: Array<{ msg: ChatMessage; used: boolean }>
}

function indexStreaming(streamingMessages: ChatMessage[]): StreamingIndex {
  const assistants: AssistantEntry[] = []
  const toolCallIds = new Set<string>()
  const toolByCallId = new Map<string, ChatMessage>()
  const optimisticUsers: Array<{ msg: ChatMessage; used: boolean }> = []

  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
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

function contentConfirmed(content: string, candidate: string): boolean {
  const prefix = content.slice(0, 200)
  return prefix.length > 0 && candidate.startsWith(prefix)
}

const assistantIdentity = (m: ChatMessage): string =>
  assistantIdentityText(m.content, m.reasoningContent).trim()

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

function assistantAlreadyPersisted(msg: ChatMessage, candidate: ChatMessage): boolean {
  const identity = assistantIdentity(msg)
  if (identity === '') return false
  return contentConfirmed(identity, assistantIdentity(candidate))
}

type BasePassResult = {
  filteredBase: ChatMessage[]
  consumedToolIds: Set<string>
}

function buildFilteredBase(baseMessages: ChatMessage[], index: StreamingIndex): BasePassResult {
  const consumedToolIds = new Set<string>()
  const filteredBase: ChatMessage[] = []
  let optimisticUserIdx = 0
  const baseIdentities = new Set(
    baseMessages.filter((m) => m.role === 'assistant').map(assistantIdentity),
  )

  for (const msg of baseMessages) {
    if (msg.role === 'assistant') {
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

    filteredBase.push(msg)
  }

  for (const e of index.assistants) {
    if (e.used || e.isStreaming) continue
    if (assistantIdentityText(e.msg.content, e.msg.reasoningContent).trim() === '') continue
    const alreadyRendered = baseMessages.some(
      (bm) => bm.role === 'assistant' && assistantAlreadyPersisted(e.msg, bm),
    )
    if (alreadyRendered) e.used = true
  }

  return { filteredBase, consumedToolIds }
}

export const ATTACHMENTS_ONLY_MARKER = '## Attachments'

export function isAttachmentsOnly(msg: ChatMessage): boolean {
  return msg.role === 'user' && msg.content.trim() === '' && (msg.attachments?.length ?? 0) > 0
}

export function isAttachmentsOnlyBase(bm: ChatMessage): boolean {
  if (bm.role !== 'user' || bm.optimistic) return false
  if ((bm.attachments?.length ?? 0) > 0) return bm.content.trim() === ''
  return bm.content.startsWith(ATTACHMENTS_ONLY_MARKER)
}

const EMPTY_CLAIMS: ReadonlySet<number> = new Set<number>()

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

export function findConfirmingBaseUserIndex(
  msg: ChatMessage,
  baseMessages: ChatMessage[],
  baseUserCount: number,
  claimed: ReadonlySet<number>,
): number {
  const candidates = collectConfirmCandidates(msg, baseMessages, claimed)
  if (candidates.length === 0) return -1

  if (baseUserCount > (msg.optimisticBaseCount ?? 0)) return candidates[0]

  const anchorId = msg.optimisticAnchorId
  if (typeof anchorId === 'string' && anchorId.length > 0) {
    const anchorIdx = baseMessages.findIndex((m) => m.id === anchorId)
    if (anchorIdx === -1) {
      return candidates[candidates.length - 1]
    }
    const after = candidates.filter((i) => i > anchorIdx)
    if (after.length > 0) return after[0]
  }

  return -1
}

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

export function findUnclaimedBaseUserForStaleBubble(
  msg: ChatMessage,
  baseMessages: ChatMessage[],
  baseUserCount: number,
  claimed: ReadonlySet<number>,
  reserved?: ReadonlySet<number>,
): number {
  if ((msg.attachments?.length ?? 0) > 0) return -1

  const claimable = (i: number): boolean => {
    if (claimed.has(i)) return false
    if (reserved?.has(i)) return false
    const bm = baseMessages[i]
    if (bm.role !== 'user' || bm.optimistic) return false
    if (isHiddenFromChat(bm)) return false
    if (parseSystemNotice(bm.content)) return false
    return true
  }

  const snapshot = msg.optimisticBaseCount ?? 0

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
    return unclaimed.length > 0 ? unclaimed[unclaimed.length - 1] : -1
  }
  for (const i of unclaimed) {
    if (i > anchorIdx) return i
  }
  return -1
}

function filterStreamingLeftovers(
  streamingMessages: ChatMessage[],
  index: StreamingIndex,
  baseMessages: ChatMessage[],
  filteredBase: ChatMessage[],
  consumedToolIds: Set<string>,
): ChatMessage[] {
  const baseUserCount = baseMessages.filter((m) => m.role === 'user' && !m.optimistic).length
  const usedAssistantIds = new Set(index.assistants.filter((e) => e.used).map((e) => e.msg.id))

  const bubbles = streamingMessages
    .filter((m) => m.role === 'user' && m.optimistic)
    .map((msg) => ({ msg, candidates: collectConfirmCandidates(msg, baseMessages, EMPTY_CLAIMS) }))

  const suffixReserved: ReadonlySet<number>[] = new Array<ReadonlySet<number>>(bubbles.length)
  const running: Set<number> = new Set<number>()
  for (let i = bubbles.length - 1; i >= 0; i--) {
    suffixReserved[i] = new Set(running)
    for (const idx of bubbles[i].candidates) running.add(idx)
  }
  const bubblePositions = new Map<string, number>()
  bubbles.forEach((b, i) => bubblePositions.set(b.msg.id, i))

  const claimed = new Set<number>()

  const kept = streamingMessages.filter((msg) => {
    if (msg.role === 'user') {
      if (!msg.optimistic) return true
      const confirmIdx = findConfirmingBaseUserIndex(msg, baseMessages, baseUserCount, claimed)
      if (confirmIdx !== -1) {
        claimed.add(confirmIdx)
        return false
      }
      if (msg.failed) return true
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
      if (hasRowPastSendPosition(msg, baseMessages, futureReserved)) return false
      if (
        baseUserCount === 0 &&
        (msg.optimisticBaseCount ?? 0) === 0 &&
        !msg.optimisticAnchorId &&
        baseMessages.some((bm) => bm.role === 'assistant')
      ) {
        return false
      }
      return true
    }

    if (msg.role === 'assistant') {
      if (usedAssistantIds.has(msg.id)) return false
      if (!msg.streaming && msg.content.trim() === '' && !msg.reasoningContent) {
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

  return enforceUniqueRenderKeys([...filteredBase, ...filteredStreaming])
}
