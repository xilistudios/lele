export function assistantIdentityText(
  content: string | undefined,
  reasoningContent?: string,
): string {
  const text = content ?? ''
  return text.trim() === '' ? (reasoningContent ?? '') : text
}
