import type { Attachment, ChatMessage, HistoryToolCall, ToolMessageStatus } from './types'

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

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

const ATTACHMENTS_HEADER = '## Attachments'

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
  }
}

export function createToolMessage(props: ToolMessageProps): ChatMessage {
  return {
    id: props.id,
    role: 'tool',
    content: '', // tool messages use toolResult, not content
    streaming: false,
    createdAt: props.createdAt ?? new Date().toISOString(),
    sessionKey: props.sessionKey,
    toolName: props.toolName,
    toolArgs: props.toolArgs,
    toolResult: props.toolResult,
    toolStatus: props.toolStatus,
    subagentSessionKey: props.subagentSessionKey,
  }
}
