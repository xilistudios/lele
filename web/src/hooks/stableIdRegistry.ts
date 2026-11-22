const MAX_ENTRIES = 500
const PREFIX_LEN = 200

const registry = new Map<string, string[]>()

export function stableIdKey(role: string, content: string): string {
  return `${role}:${content.slice(0, PREFIX_LEN)}`
}

export function registerStableId(role: string, content: string, ephemeralId: string): void {
  if (!ephemeralId || !content) return
  const key = stableIdKey(role, content)
  let ids = registry.get(key)
  if (!ids) {
    ids = []
    registry.set(key, ids)
  }
  if (!ids.includes(ephemeralId)) {
    ids.push(ephemeralId)
  }
  registry.delete(key)
  registry.set(key, ids)
  if (registry.size > MAX_ENTRIES) {
    const oldest = registry.keys().next().value
    if (oldest !== undefined) registry.delete(oldest)
  }
}

export function lookupStableId(role: string, content: string, occurrence = 0): string | undefined {
  return registry.get(stableIdKey(role, content))?.[occurrence]
}

export function lookupStableIdFromEnd(
  role: string,
  content: string,
  fromEnd = 0,
): string | undefined {
  const ids = registry.get(stableIdKey(role, content))
  if (!ids || ids.length === 0) return undefined
  const index = ids.length - 1 - fromEnd
  return index >= 0 ? ids[index] : undefined
}

export function countStableIds(role: string, content: string): number {
  return registry.get(stableIdKey(role, content))?.length ?? 0
}

export function clearStableIdRegistry(): void {
  registry.clear()
}
