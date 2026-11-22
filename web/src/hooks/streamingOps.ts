import { createAssistantMessage } from '../lib/chatMessageBuilder'
import type { ChatMessage } from '../lib/types'
import { sessionKeysLooselyMatch } from './event-handlers/helpers'
import { computeAssistantInsertIndex } from './messageInsertion'
import { isAttachmentsOnly } from './messageMerge'

const RESTORE_PREFIX = 'restore-'

function isRestorePlaceholder(m: ChatMessage, sessionKey: string): boolean {
  return m.id.startsWith(RESTORE_PREFIX) && m.sessionKey === sessionKey
}

export function migrateRestoreId(
  current: ChatMessage[],
  msgId: string,
  sessionKey: string,
): ChatMessage[] {
  if (current.some((m) => m.id === msgId)) return current
  const restoreIdx = current.findIndex((m) => isRestorePlaceholder(m, sessionKey))
  if (restoreIdx < 0) return current
  return current.map((m, i) => (i === restoreIdx ? { ...m, id: msgId } : m))
}

export function removeRestorePlaceholders(
  current: ChatMessage[],
  sessionKey: string,
): ChatMessage[] {
  return current.filter((m) => !isRestorePlaceholder(m, sessionKey))
}

export function upsertAssistantPlaceholder(
  current: ChatMessage[],
  msgId: string,
  sessionKey: string,
): ChatMessage[] {
  if (current.some((m) => m.id === msgId)) return current

  const newMsg = createAssistantMessage({
    id: msgId,
    sessionKey,
    content: '',
    streaming: true,
  })
  const arr = [...current]
  arr.splice(computeAssistantInsertIndex(current), 0, newMsg)
  return arr
}

export function restoreInProgressAssistant(
  current: ChatMessage[],
  sessionKey: string,
  content: string,
  reasoning: string,
  insertAfterLastUserForSession: boolean,
): ChatMessage[] {
  const restoreId = `${RESTORE_PREFIX}${sessionKey}`
  const existingIdx = current.findIndex((m) => m.id === restoreId)

  if (existingIdx >= 0) {
    return current.map((m, i) =>
      i === existingIdx
        ? {
            ...m,
            content: content || m.content,
            reasoningContent: reasoning || m.reasoningContent,
            streaming: true,
          }
        : m,
    )
  }

  const newMsg = createAssistantMessage({
    id: restoreId,
    sessionKey,
    content,
    reasoningContent: reasoning || undefined,
    streaming: true,
  })

  const arr = [...current]
  if (insertAfterLastUserForSession) {
    let insertIdx = arr.length
    for (let i = arr.length - 1; i >= 0; i--) {
      if (arr[i].sessionKey === sessionKey && arr[i].role === 'user') {
        insertIdx = i + 1
        break
      }
    }
    arr.splice(insertIdx, 0, newMsg)
  } else {
    arr.push(newMsg)
  }
  return arr
}

export function applyMessageComplete(
  current: ChatMessage[],
  msgId: string,
  sessionKey: string,
  serverContent: string | undefined,
  serverAttachments?: ChatMessage['attachments'],
  serverError?: boolean,
): ChatMessage[] {
  const hasServerAttachments = !!serverAttachments && serverAttachments.length > 0

  if (!current.some((m) => m.role === 'assistant' && m.id === msgId)) {
    const content = serverContent && serverContent.length > 0 ? serverContent : ''
    const newMsg = createAssistantMessage({
      id: msgId,
      sessionKey,
      content,
      streaming: false,
      attachments: hasServerAttachments ? serverAttachments : undefined,
      error: serverError ? content : undefined,
    })
    const arr = [...current]
    arr.splice(computeAssistantInsertIndex(current), 0, newMsg)
    return arr.filter((m) => !isRestorePlaceholder(m, sessionKey))
  }

  return current.flatMap((m) => {
    if (isRestorePlaceholder(m, sessionKey)) return []

    if (m.role === 'assistant' && m.id === msgId) {
      const content = serverContent && serverContent.length > 0 ? serverContent : m.content
      return [
        {
          ...m,
          content,
          streaming: false,
          ...(serverError ? { error: content } : {}),
          ...(hasServerAttachments ? { attachments: serverAttachments } : {}),
        },
      ]
    }
    if (m.role === 'user' && m.sessionKey === sessionKey && m.content.trim() === '') {
      return []
    }
    if (m.role === 'tool' && m.sessionKey === sessionKey) {
      return [{ ...m, streaming: false }]
    }
    return [m]
  })
}

export function markOptimisticUserFailed(
  current: ChatMessage[],
  sessionKey: string,
): ChatMessage[] {
  return current.map((m) =>
    m.role === 'user' && m.optimistic && m.sessionKey === sessionKey
      ? { ...m, failed: true, streaming: false }
      : m,
  )
}

export function markNewestOptimisticUserFailed(
  current: ChatMessage[],
  sessionKey: string,
): ChatMessage[] {
  for (let i = current.length - 1; i >= 0; i--) {
    const m = current[i]
    if (m.role !== 'user' || !m.optimistic || !sessionKeysLooselyMatch(m.sessionKey, sessionKey))
      continue
    if (m.failed === true) return current
    const next = [...current]
    next[i] = { ...m, failed: true, streaming: false }
    return next
  }
  return current
}

export function retractAttachmentsOnlyBubble(
  current: ChatMessage[],
  sessionKey: string | null,
): { messages: ChatMessage[]; restoredPaths: string[] } | null {
  const index = current.findIndex(
    (m) =>
      m.role === 'user' &&
      m.optimistic === true &&
      (sessionKey === null || m.sessionKey === sessionKey) &&
      isAttachmentsOnly(m),
  )
  if (index < 0) return null

  const restoredPaths = (current[index].attachments ?? [])
    .map((attachment) => attachment.path)
    .filter((path): path is string => Boolean(path))

  return { messages: current.filter((_, i) => i !== index), restoredPaths }
}

export function removeDiscardedMessage(
  current: ChatMessage[],
  messageID: string,
  sessionKey: string,
): ChatMessage[] {
  const index = current.findIndex(
    (m) =>
      m.id === messageID &&
      m.role === 'assistant' &&
      sessionKeysLooselyMatch(m.sessionKey, sessionKey),
  )
  if (index < 0) return current
  return current.filter((_, i) => i !== index)
}

export function markStreamingAssistantsErrored(
  current: ChatMessage[],
  sessionKey: string,
  error: string,
): ChatMessage[] {
  return current.map((m) =>
    m.sessionKey === sessionKey && m.role === 'assistant' && m.streaming
      ? { ...m, streaming: false, error }
      : m,
  )
}

export function stopAllStreaming(current: ChatMessage[]): ChatMessage[] {
  return current.map((m) => ({ ...m, streaming: false }))
}

export function attachToLastAssistant(
  current: ChatMessage[],
  attachments: ChatMessage['attachments'],
): ChatMessage[] {
  const idx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  if (idx < 0) return current
  const targetIndex = current.length - idx - 1
  return current.map((m, i) => (i === targetIndex ? { ...m, attachments, streaming: false } : m))
}
