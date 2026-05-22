/**
 * Generates a UUID v4 string compatible across all contexts (HTTP included).
 *
 * `crypto.randomUUID()` is only available in secure contexts (HTTPS or localhost).
 * This falls back to `crypto.getRandomValues()` which works in all contexts,
 * and a Math.random-based fallback as last resort.
 */
export function generateUUID(): string {
  // Try the standard crypto.randomUUID first (secure contexts)
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }

  // Fallback: crypto.getRandomValues (works in most browsers, including HTTP)
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = new Uint8Array(16)
    crypto.getRandomValues(bytes)
    // Set version 4 (0100 in binary) and variant bits
    bytes[6] = (bytes[6] & 0x0f) | 0x40
    bytes[8] = (bytes[8] & 0x3f) | 0x80
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`
  }

  // Last resort: Math.random (not cryptographically secure but works everywhere)
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    return (c === 'x' ? r : (r & 0x3) | 0x8).toString(16)
  })
}
