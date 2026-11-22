import type { ChatMessage } from './types'

export function isThinkingOnlyRow(m: ChatMessage): boolean {
  if (m.role !== 'assistant') return false
  if (m.content.trim() !== '') return false
  if ((m.reasoningContent ?? '').trim() === '') return false
  if ((m.attachments?.length ?? 0) !== 0) return false
  if (m.excludeFromContext) return false
  return true
}

function collapseRun(messages: ChatMessage[], start: number, end: number): ChatMessage {
  const first = messages[start]
  const texts: string[] = []
  let streaming = false
  for (let i = start; i < end; i++) {
    if (messages[i].streaming === true) streaming = true
    const text = (messages[i].reasoningContent ?? '').trim()
    if (text === '') continue
    if (texts.length > 0 && texts[texts.length - 1] === text) continue
    texts.push(text)
  }
  return {
    ...first,
    reasoningContent: texts.join('\n\n'),
    streaming,
    stableId: first.stableId ?? first.id,
  }
}

export function collapseThinkingRuns(messages: ChatMessage[]): ChatMessage[] {
  let collapsed: ChatMessage[] | null = null
  let i = 0
  while (i < messages.length) {
    if (!isThinkingOnlyRow(messages[i])) {
      if (collapsed) collapsed.push(messages[i])
      i++
      continue
    }

    let end = i + 1
    while (end < messages.length && isThinkingOnlyRow(messages[end])) end++

    if (end - i === 1) {
      if (collapsed) collapsed.push(messages[i])
      i = end
      continue
    }

    if (!collapsed) collapsed = messages.slice(0, i)

    collapsed.push(collapseRun(messages, i, end))

    i = end
  }

  return collapsed ?? messages
}
