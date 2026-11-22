import {
  countStableIds,
  lookupStableId,
  lookupStableIdFromEnd,
  stableIdKey,
} from '../hooks/stableIdRegistry'
import { assistantIdentityText } from './messageIdentity'
import type {
  Attachment,
  ChatMessage,
  HistoryCommandInfo,
  HistoryToolCall,
  RawHistoryMessage,
  ToolMessageStatus,
} from './types'

export function createHistoryMessageId(sessionKey: string, index: number, role: string): string {
  return `${sessionKey}:${index}:${role}`
}

let lastOptimisticTs = 0

export function createOptimisticUserId(): string {
  const now = Date.now()
  lastOptimisticTs = now > lastOptimisticTs ? now : lastOptimisticTs + 1
  return `temp-user-${lastOptimisticTs}`
}

export function createToolMessageId(toolName: string, suffix?: string): string {
  return `tool-${toolName}-${suffix ?? Date.now()}`
}

export function createDeterministicToolMessageId(messageId: string, toolCallId: string): string {
  return `tool:${messageId}:${toolCallId}`
}

const ATTACHMENTS_HEADER = '## Attachments'

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

export function isHiddenFromChat(message: { role: ChatMessage['role']; content: string }): boolean {
  if (message.content.startsWith('⚠️ GUIDANCE:')) return true
  if (message.content.startsWith('GUIDANCE:')) return true
  return isCompactionSummary(message)
}

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

export function formatToolCallArgs(toolCall: HistoryToolCall): string {
  if (typeof toolCall.arguments === 'undefined') {
    return toolCall.name ?? ''
  }

  return toolCall.name
    ? `${toolCall.name} ${JSON.stringify(toolCall.arguments)}`
    : JSON.stringify(toolCall.arguments)
}

export const SYSTEM_NOTICE_TOOL_NAME = 'system_notice'

export function parseSystemNotice(content: string): { sender: string; body: string } | undefined {
  const match = /^\[System:\s*([^\]]+)\]\s*([\s\S]*)$/.exec(content ?? '')
  if (!match) return undefined
  const sender = match[1].trim()
  if (sender === '') return undefined
  return { sender, body: match[2].trim() }
}

export const COMMAND_TOOL_NAME = 'command'

export function normalizeCommandArgs(cmd: {
  command: string
  args?: string
  agent?: string
  model?: string
  source?: string
  description?: string
}): Record<string, string> {
  const name = (cmd.command ?? '').trim()
  return {
    command: name.startsWith('/') ? name : `/${name}`,
    args: cmd.args ?? '',
    agent: cmd.agent ?? '',
    model: cmd.model ?? '',
    source: cmd.source ?? '',
    description: cmd.description ?? '',
  }
}

export function buildCommandChipToolArgs(cmd: HistoryCommandInfo): string {
  return `${COMMAND_TOOL_NAME} ${JSON.stringify(normalizeCommandArgs(cmd))}`
}

export function buildCommandChipFromHistory(
  message: RawHistoryMessage,
  msgId: string,
  sessionKey: string,
): ChatMessage | undefined {
  const cmd = message.harness_command
  if (!cmd?.command) return undefined
  return createToolMessage({
    id: `command:${msgId}`,
    sessionKey,
    toolName: COMMAND_TOOL_NAME,
    toolArgs: buildCommandChipToolArgs(cmd),
    toolStatus: 'completed',
  })
}

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

interface BaseMessageProps {
  id: string
  sessionKey: string
  createdAt?: string
  attachments?: Attachment[]
  excludeFromContext?: boolean
  stableId?: string
}

interface UserMessageProps extends BaseMessageProps {
  content: string
  optimistic?: boolean
  optimisticBaseCount?: number
  optimisticAnchorId?: string
  systemNotice?: { sender: string; body: string }
}

interface AssistantMessageProps extends BaseMessageProps {
  content: string
  reasoningContent?: string
  streaming?: boolean
  error?: string
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
    optimisticAnchorId: props.optimisticAnchorId,
    systemNotice: props.systemNotice,
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
    error: props.error,
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
    attachments: props.attachments,
    toolName: props.toolName,
    toolArgs: props.toolArgs,
    toolResult: props.toolResult,
    toolStatus: props.toolStatus,
    toolCallId: props.toolCallId,
    subagentSessionKey: props.subagentSessionKey,
    excludeFromContext: props.excludeFromContext,
    stableId: props.stableId ?? props.id,
  }
}

function historyStableContent(message: RawHistoryMessage): string {
  return (
    message.display_content ||
    (message.role === 'assistant'
      ? assistantIdentityText(message.content, message.reasoning_content)
      : message.content)
  )
}

function countStableIdentities(history: RawHistoryMessage[]): Map<string, number> {
  const totals = new Map<string, number>()
  for (const message of history) {
    const key = stableIdKey(message.role, historyStableContent(message))
    totals.set(key, (totals.get(key) ?? 0) + 1)
  }
  return totals
}

export type ToChatMessagesOptions = {
  bindStableIds?: boolean
}

export function toChatMessages(
  history: RawHistoryMessage[],
  sessionKey: string,
  options: ToChatMessagesOptions = {},
): ChatMessage[] {
  const toolCallMap = buildToolCallMap(history)
  const seenIds = new Map<string, number>()
  const bindStableIds = options.bindStableIds === true

  const stableOccurrences = new Map<string, number>()
  const stableTotals = countStableIdentities(history)

  return history.flatMap((message, index) => {
    let msgId = message.id || createHistoryMessageId(sessionKey, index, message.role)

    const count = seenIds.get(msgId) ?? 0
    seenIds.set(msgId, count + 1)
    if (count > 0) {
      msgId = `${msgId}_${count}`
    }

    let messageContent = message.content
    let parsedAttachments: undefined | ReturnType<typeof parseAttachmentsFromContent>['attachments']

    const stableContent = historyStableContent(message)
    const stableKey = stableIdKey(message.role, stableContent)
    const stableOccurrence = stableOccurrences.get(stableKey) ?? 0
    stableOccurrences.set(stableKey, stableOccurrence + 1)
    const copiesInPage = stableTotals.get(stableKey) ?? 1
    const fromEnd = copiesInPage - 1 - stableOccurrence
    const rankBinding = bindStableIds && countStableIds(message.role, stableContent) >= copiesInPage
    const toolStableId =
      message.role === 'tool' && message.tool_call_id
        ? lookupStableId('tool', `id:${message.tool_call_id}`)
        : undefined
    const stableId =
      toolStableId ??
      (rankBinding ? lookupStableIdFromEnd(message.role, stableContent, fromEnd) : undefined)

    if (message.role === 'user') {
      const parsed = parseAttachmentsFromContent(messageContent)
      messageContent = parsed.content
      if (parsed.attachments.length > 0) {
        parsedAttachments = parsed.attachments
      }
    }

    if (message.attachments?.length) {
      parsedAttachments = message.attachments
    }

    if (message.role === 'user') {
      const userContent = message.display_content || messageContent
      const systemNotice = parseSystemNotice(userContent)

      const userMessage = createUserMessage({
        id: msgId,
        sessionKey,
        content: userContent,
        attachments: parsedAttachments,
        excludeFromContext: message.exclude_from_context,
        stableId,
        systemNotice,
      })

      const chip = buildCommandChipFromHistory(message, msgId, sessionKey)
      return chip ? [userMessage, chip] : [userMessage]
    }

    if (message.role === 'assistant') {
      const hasToolCalls = message.tool_calls && message.tool_calls.length > 0
      const hasReasoning = Boolean(
        message.reasoning_content && message.reasoning_content.trim() !== '',
      )
      const hasContent = Boolean(messageContent && messageContent.trim() !== '')
      const errorDetail = message.error_detail || undefined
      const shouldAddAssistant = hasContent || hasReasoning || !hasToolCalls || Boolean(errorDetail)
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
            error: errorDetail,
          }),
        ]
      }
      return []
    }

    if (message.role === 'tool') {
      if (message.tool_call_id?.startsWith('approval:')) {
        return [
          {
            id: msgId,
            role: 'assistant' as const,
            content: message.content,
            streaming: false,
            createdAt: new Date().toISOString(),
            sessionKey,
            attachments: parsedAttachments,
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
          attachments: parsedAttachments,
          stableId,
        }),
      ]
    }

    return []
  })
}
