const KIB = 1024

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B'
  if (bytes < KIB) return `${Math.round(bytes)} B`

  const units = ['KB', 'MB', 'GB'] as const
  let value = bytes / KIB
  let unitIndex = 0

  while (value >= KIB && unitIndex < units.length - 1) {
    value /= KIB
    unitIndex++
  }

  const rounded = Number(value.toFixed(1))
  if (rounded >= KIB && unitIndex < units.length - 1) {
    return `${(rounded / KIB).toFixed(1)} ${units[unitIndex + 1]}`
  }

  return `${rounded.toFixed(1)} ${units[unitIndex]}`
}
