import type { Attachment, ChatMessage, HistoryToolCall, ToolMessageStatus } from './types'
import { lookupStableId } from '../hooks/stableIdRegistry'

// ---------------------------------------------------------------------------
// ID generators
// ---------------------------------------------------------------------------

/** Deterministic ID for history-sourced messages (stable across refetches). */
export function createHistoryMessageId(sessionKey: string, index: number, role: string): string {
  return `${sessionKey}:${index}:${role}`
}

/** Ephemeral ID for optimistic user messages. */
export function createOptimisticUserId(): string {
  return `temp-user-${Date.now()}`
}

/** Ephemeral ID for tool execution messages in streaming. */
export function createToolMessageId(toolName: string, suffix?: string): string {
  return `tool-${toolName}-${suffix ?? Date.now()}`
}

export function createDeterministicToolMessageId(messageId: string, toolCallId: string): string {
  return `tool:${messageId}:${toolCallId}`
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

const ATTACHMENTS_HEADER = '## Attachments'

/**
 * True if a message is an internal context-compaction summary that must be
 * hidden from the chat UI.
 *
 * Mirrors the TUI guard in `pkg/tui/viewport.go` (isCompactionSummary):
 * these are synthetic "user" messages inserted by the backend when the
 * context window is compacted, e.g.
 *
 *   ## Summary of Previous Conversation\n\n...
 *   [Context compacted — summary of previous N messages]\n...
 *
 * They carry `exclude_from_context`, so they may also be filtered by flag,
 * but the content-prefix guard is the authoritative signal (the flag is
 * also set on user-excluded attachments). Showing the summary as a real
 * user bubble is confusing — the user never sent it.
 */
export function isCompactionSummary(message: {
  role: ChatMessage['role']
  content: string
}): boolean {
  if (message.role !== 'user') return false
  return (
    message.content.startsWith('## Summary of Previous Conversation\n\n') ||
    message.content.startsWith('[Context compacted —')
  )
}

/**
 * Parses attachment paths from a message content string that contains
 * "## Attachments\n- /path/file.png" appended by the backend.
 * Returns the cleaned content and extracted attachment info.
 */
export function parseAttachmentsFromContent(content: string): {
  content: string
  attachments: Attachment[]
} {
  if (!content.includes(ATTACHMENTS_HEADER)) {
    return { content, attachments: [] }
  }

  const lines = content.split('\n')
  const headerIndex = lines.findIndex((line) => line.trim() === ATTACHMENTS_HEADER)
  if (headerIndex === -1) {
    return { content, attachments: [] }
  }

  const cleanContent = lines.slice(0, headerIndex).join('\n').trim()
  const attachmentLines = lines.slice(headerIndex + 1)
  const attachments: Attachment[] = []

  for (const line of attachmentLines) {
    const trimmed = line.trim()
    if (trimmed.startsWith('- ')) {
      const path = trimmed.slice(2).trim()
      if (path) {
        attachments.push({
          path,
          name: path.split('/').pop() ?? path,
          mime_type: undefined,
          kind: 'file',
        })
      }
    }
  }

  return { content: cleanContent, attachments }
}

/**
 * Extracts a subagent session key from a spawn tool result string.
 * Handles formats like "Spawned subagent task task-1 (...)" and "task id: abc".
 */
export function parseSubagentSessionKey(value: string | undefined): string | undefined {
  if (!value) return undefined

  const trimmed = value.trim()
  if (trimmed === '') return undefined

  const directMatch = trimmed.match(/subagent:([A-Za-z0-9_-]+)/i)
  if (directMatch) {
    return `subagent:${directMatch[1]}`
  }

  const taskMatch = trimmed.match(/\btask(?:\s+id)?\s*:?[ \t]+([A-Za-z0-9_-]+)/i)
  if (taskMatch) {
    return `subagent:${taskMatch[1]}`
  }

  return undefined
}

/** Formats a HistoryToolCall into a human-readable string for toolArgs. */
export function formatToolCallArgs(toolCall: HistoryToolCall): string {
  if (typeof toolCall.arguments === 'undefined') {
    return toolCall.name ?? ''
  }

  return toolCall.name
    ? `${toolCall.name} ${JSON.stringify(toolCall.arguments)}`
    : JSON.stringify(toolCall.arguments)
}

/** Builds a map of tool_call_id → HistoryToolCall from the history array. */
export function buildToolCallMap(
  history: Array<{
    tool_calls?: HistoryToolCall[]
  }>,
): Map<string, HistoryToolCall> {
  const map = new Map<string, HistoryToolCall>()
  for (const message of history) {
    if (message.tool_calls?.length) {
      for (const tc of message.tool_calls) {
        if (tc.id) {
          map.set(tc.id, tc)
        }
      }
    }
  }
  return map
}

// ---------------------------------------------------------------------------
// Message factory functions
// ---------------------------------------------------------------------------

interface BaseMessageProps {
  id: string
  sessionKey: string
  createdAt?: string
  attachments?: Attachment[]
  excludeFromContext?: boolean
  /** Durable render-key identity carried over from the streaming copy. */
  stableId?: string
}

interface UserMessageProps extends BaseMessageProps {
  content: string
  optimistic?: boolean
  optimisticBaseCount?: number
}

interface AssistantMessageProps extends BaseMessageProps {
  content: string
  reasoningContent?: string
  streaming?: boolean
}

interface ToolMessageProps extends BaseMessageProps {
  toolName: string
  toolArgs?: string
  toolResult?: string
  toolStatus: ToolMessageStatus
  toolCallId?: string
  subagentSessionKey?: string
}

export function createUserMessage(props: UserMessageProps): ChatMessage {
  return {
    id: props.id,
    role: 'user',
    content: props.content,
    streaming: false,
    createdAt: props.createdAt ?? new Date().toISOString(),
    sessionKey: props.sessionKey,
    attachments: props.attachments,
    optimistic: props.optimistic,
    optimisticBaseCount: props.optimisticBaseCount,
    excludeFromContext: props.excludeFromContext,
    stableId: props.stableId,
  }
}

export function createAssistantMessage(props: AssistantMessageProps): ChatMessage {
  return {
    id: props.id,
    role: 'assistant',
    content: props.content,
    reasoningContent: props.reasoningContent,
    streaming: props.streaming ?? false,
    createdAt: props.createdAt ?? new Date().toISOString(),
    sessionKey: props.sessionKey,
    attachments: props.attachments,
    excludeFromContext: props.excludeFromContext,
    stableId: props.stableId,
  }
}

export function createToolMessage(props: ToolMessageProps): ChatMessage {
  return {
    id: props.id,
    role: 'tool',
    content: '',
    streaming: false,
    createdAt: props.createdAt ?? new Date().toISOString(),
    sessionKey: props.sessionKey,
    toolName: props.toolName,
    toolArgs: props.toolArgs,
    toolResult: props.toolResult,
    toolStatus: props.toolStatus,
    toolCallId: props.toolCallId,
    subagentSessionKey: props.subagentSessionKey,
    excludeFromContext: props.excludeFromContext,
  }
}

// ---------------------------------------------------------------------------
// History-to-chat conversion
// ---------------------------------------------------------------------------

/**
 * Converts raw history messages from the API into ChatMessage objects.
 * Handles attachment parsing, tool call mapping, and approval message formatting.
 */
export function toChatMessages(
  history: Array<{
    id: string
    role: 'user' | 'assistant' | 'tool'
    content: string
    reasoning_content?: string
    tool_calls?: HistoryToolCall[]
    tool_call_id?: string
    tool_name?: string
    exclude_from_context?: boolean
  }>,
  sessionKey: string,
): ChatMessage[] {
  const toolCallMap = buildToolCallMap(history)
  const seenIds = new Map<string, number>()
  // Signature (role|content|tool_call_id) of the first message seen under each
  // id, used to detect exact duplicates below.
  const seenSignatures = new Map<string, string>()

  return history.flatMap((message, index) => {
    let msgId = message.id || createHistoryMessageId(sessionKey, index, message.role)
    const signature = `${message.role}|${message.content}|${message.tool_call_id ?? ''}`
    const count = seenIds.get(msgId) ?? 0
    seenIds.set(msgId, count + 1)
    if (count > 0) {
      // Exact duplicate (same id, role and content): the backend re-emitted
      // the same message. Suffixing it renders a visible duplicate bubble that
      // later disappears or swaps position when the list is rebuilt (flicker).
      // Drop it and keep the first occurrence instead.
      if (seenSignatures.get(msgId) === signature) {
        return []
      }
      // Different content under the same id: keep both, disambiguate by suffix.
      msgId = `${msgId}_${count}`
    } else {
      seenSignatures.set(msgId, signature)
    }

    let messageContent = message.content
    let parsedAttachments: undefined | ReturnType<typeof parseAttachmentsFromContent>['attachments']

    // Re-attach a durable stableId recorded when the streaming (ephemeral)
    // copy of this message was confirmed into history. This keeps React
    // render keys identical across the WebSocket→HTTP transition and across
    // subsequent refetches (prevents remount flicker). See stableIdRegistry.ts.
    const stableId = lookupStableId(message.role, messageContent)

    if (message.role === 'user') {
      const parsed = parseAttachmentsFromContent(messageContent)
      messageContent = parsed.content
      if (parsed.attachments.length > 0) {
        parsedAttachments = parsed.attachments
      }
    }

    if (message.role === 'user') {
      return [
        createUserMessage({
          id: msgId,
          sessionKey,
          content: messageContent,
          attachments: parsedAttachments,
          excludeFromContext: message.exclude_from_context,
          stableId,
        }),
      ]
    }

    if (message.role === 'assistant') {
      const hasToolCalls = message.tool_calls && message.tool_calls.length > 0
      const hasReasoning = Boolean(
        message.reasoning_content && message.reasoning_content.trim() !== '',
      )
      const hasContent = Boolean(messageContent && messageContent.trim() !== '')
      const shouldAddAssistant = hasContent || hasReasoning || !hasToolCalls
      if (shouldAddAssistant) {
        return [
          createAssistantMessage({
            id: msgId,
            sessionKey,
            content: messageContent,
            reasoningContent: message.reasoning_content,
            streaming: false,
            attachments: parsedAttachments,
            excludeFromContext: message.exclude_from_context,
            stableId,
          }),
        ]
      }
      return []
    }

    if (message.role === 'tool') {
      // Approval messages should render as simple text, not tool cards
      if (message.tool_call_id?.startsWith('approval:')) {
        return [
          {
            id: msgId,
            role: 'assistant' as const,
            content: message.content,
            streaming: false,
            createdAt: new Date().toISOString(),
            sessionKey,
          },
        ]
      }

      const matchedToolCall = message.tool_call_id
        ? toolCallMap.get(message.tool_call_id)
        : undefined
      const toolName = matchedToolCall?.name ?? message.tool_name ?? message.tool_call_id ?? 'tool'
      const toolArgs = matchedToolCall ? formatToolCallArgs(matchedToolCall) : ''
      const subagentSessionKey =
        toolName === 'spawn' ? parseSubagentSessionKey(message.content) : undefined

      return [
        createToolMessage({
          id: msgId,
          sessionKey,
          toolName,
          toolArgs,
          toolResult: message.content,
          toolStatus: 'completed',
          toolCallId: message.tool_call_id,
          subagentSessionKey,
        }),
      ]
    }

    return []
  })
}
