import type { Attachment } from './types'

export const IMAGE_EXTENSIONS: ReadonlySet<string> = new Set([
  '.png',
  '.jpg',
  '.jpeg',
  '.gif',
  '.webp',
  '.bmp',
  '.svg',
])

export function isImageExtension(name: string): boolean {
  const ext = name.toLowerCase().split('.').pop()
  return ext ? IMAGE_EXTENSIONS.has(`.${ext}`) : false
}

export function isImageAttachment(attachment: Attachment): boolean {
  if (attachment.mime_type?.startsWith('image/')) return true
  if (attachment.name && isImageExtension(attachment.name)) return true
  if (attachment.path && isImageExtension(attachment.path)) return true
  return false
}

export function imageAttachments(attachments: Attachment[] | undefined): Attachment[] {
  return (attachments ?? []).filter(isImageAttachment)
}

export function nonImageAttachments(attachments: Attachment[] | undefined): Attachment[] {
  return (attachments ?? []).filter((attachment) => !isImageAttachment(attachment))
}

export function partitionAttachments(attachments: Attachment[] | undefined): {
  images: Attachment[]
  others: Attachment[]
} {
  const images: Attachment[] = []
  const others: Attachment[] = []
  for (const attachment of attachments ?? []) {
    if (isImageAttachment(attachment)) images.push(attachment)
    else others.push(attachment)
  }
  return { images, others }
}
