/**
 * Display formatters (spec §7.9).
 *
 * Pure presentation helpers: they never round-trip back into config values.
 */

const KIB = 1024

/**
 * Human-readable byte count, binary units (1 KB = 1024 B).
 *
 * - bytes        -> integer, no decimals (`900` -> `900 B`)
 * - KB / MB / GB -> one decimal (`4200` -> `4.1 KB`)
 *
 * Anything above GB keeps the GB unit (agent workspace files never get near
 * it; the goal is a stable, short chip label, not an exhaustive scale).
 */
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

  // 1023.96 KB would render as "1024.0 KB" after one decimal: promote the unit
  // so a label never shows a value that should already be the next magnitude.
  const rounded = Number(value.toFixed(1))
  if (rounded >= KIB && unitIndex < units.length - 1) {
    return `${(rounded / KIB).toFixed(1)} ${units[unitIndex + 1]}`
  }

  return `${rounded.toFixed(1)} ${units[unitIndex]}`
}
