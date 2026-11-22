import type { ChatMessage } from '../lib/types'

export function computeAssistantInsertIndex(current: ChatMessage[]): number {
  const lastUserRevIdx = [...current].reverse().findIndex((m) => m.role === 'user')
  const lastAsstRevIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  const lastUserPos = lastUserRevIdx >= 0 ? current.length - 1 - lastUserRevIdx : -1
  const lastAsstPos = lastAsstRevIdx >= 0 ? current.length - 1 - lastAsstRevIdx : -1

  if (lastUserPos > lastAsstPos) {
    return lastUserPos + 1
  }

  if (lastAsstPos >= 0) {
    let insertIdx = lastAsstPos + 1
    while (insertIdx < current.length && current[insertIdx].role === 'tool') {
      insertIdx++
    }
    return insertIdx
  }

  const firstToolIdx = current.findIndex((m) => m.role === 'tool')
  return firstToolIdx >= 0 ? firstToolIdx : current.length
}

export function computeToolInsertIndex(current: ChatMessage[]): number {
  const lastAsstRevIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  if (lastAsstRevIdx < 0) return current.length

  const assistantIdx = current.length - 1 - lastAsstRevIdx
  let insertIdx = assistantIdx + 1
  while (insertIdx < current.length && current[insertIdx].role === 'tool') {
    insertIdx++
  }
  return insertIdx
}

export function countPrependedItems(prevFirstKey: string | undefined, nextKeys: string[]): number {
  if (!prevFirstKey) return 0
  const idx = nextKeys.indexOf(prevFirstKey)
  return idx > 0 ? idx : 0
}

export function nextFirstItemIndex(
  firstItemIndex: number,
  prevFirstKey: string | undefined,
  nextKeys: string[],
): number {
  const prepended = countPrependedItems(prevFirstKey, nextKeys)
  if (prepended <= 0) return firstItemIndex
  return Math.max(0, firstItemIndex - prepended)
}
