import type { ChatMessage } from './types'

/**
 * The single source of truth for the chat log's vertical rhythm.
 *
 * Scope: MESSAGE ROWS only — the wrappers `MessageBubble` renders. Anything the
 * list draws around them (group headers, approval cards and the "executing"
 * footer in `MessageList.tsx`) keeps its own padding and is not part of this
 * contract.
 *
 * Each row used to hardcode its own vertical padding (user `py-1`, tool and
 * system notice `py-1.5`, assistant `py-3`), so the gap between two
 * consecutive rows depended on WHICH kinds happened to be adjacent:
 * user↔tool 10px, tool↔tool 12px, tool↔text 18px, text↔text 24px,
 * user↔text 16px — visually irregular.
 *
 * With only two row kinds the gap is determined by the kinds alone and never by
 * ordering: compact↔compact 12px, compact↔content 18px, content↔content 24px.
 */
export const CHAT_ROW_PADDING = {
  compact: 'py-1.5',
  content: 'py-3',
} as const

export type ChatRowKind = 'compact' | 'content'

/**
 * Which rhythm an assistant/tool/user row takes.
 *
 * An assistant row is prose only when it carries VISIBLE text. Rows that hold
 * nothing but activity or metadata are compact: a thinking card or the typing
 * dots, an attachment-only row (chip, no text) and a banner-only row
 * (`excludeFromContext`, no text) all fall in this group, because none of them
 * has prose to give breathing room to.
 */
export function chatRowKind(message: Pick<ChatMessage, 'role' | 'content'>): ChatRowKind {
  if (message.role === 'tool') return 'compact'
  if (message.role === 'user') return 'compact'
  // Assistant rows without visible text are activity or metadata (thinking
  // card, typing dots, attachments, archived-from-context banner), not prose:
  // they must not get the prose rhythm (that was the irregularity).
  return message.content.trim() === '' ? 'compact' : 'content'
}

export function chatRowPadding(message: Pick<ChatMessage, 'role' | 'content'>): string {
  return CHAT_ROW_PADDING[chatRowKind(message)]
}
