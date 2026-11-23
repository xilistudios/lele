import type { ChatMessage } from './types'

const ATTACHMENT_IDENTITY_SEPARATOR = '\u0000'

type IdentityAttachment = { path?: string; name?: string }

export function assistantIdentityText(
  content: string | undefined,
  reasoningContent?: string,
): string {
  const text = content ?? ''
  return text.trim() === '' ? (reasoningContent ?? '') : text
}

export function attachmentIdentityKeys(attachments: IdentityAttachment[] | undefined): string[] {
  return (attachments ?? [])
    .map((attachment) => attachment.path ?? attachment.name ?? '')
    .filter(Boolean)
    .sort()
}

export function assistantMessageIdentity(
  content?: string,
  reasoningContent?: string,
  attachments?: IdentityAttachment[],
): string {
  const text = assistantIdentityText(content, reasoningContent).trim()
  if (text !== '') return text
  return attachmentIdentityKeys(attachments).join(ATTACHMENT_IDENTITY_SEPARATOR)
}

export function assistantIdentityOf(message: ChatMessage): string {
  return assistantMessageIdentity(message.content, message.reasoningContent, message.attachments)
}
