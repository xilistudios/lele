import type { ChatMessage } from './types'

export const CHAT_ROW_PADDING = {
  compact: 'py-1.5',
  content: 'py-3',
} as const

export type ChatRowKind = 'compact' | 'content'

export function chatRowKind(message: Pick<ChatMessage, 'role' | 'content'>): ChatRowKind {
  if (message.role === 'tool') return 'compact'
  if (message.role === 'user') return 'compact'
  return message.content.trim() === '' ? 'compact' : 'content'
}

export function chatRowPadding(message: Pick<ChatMessage, 'role' | 'content'>): string {
  return CHAT_ROW_PADDING[chatRowKind(message)]
}
