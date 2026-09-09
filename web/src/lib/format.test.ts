import { describe, expect, test } from 'bun:test'
import { formatBytes } from './format'

describe('formatBytes', () => {
  test('bytes are integers without decimals', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1)).toBe('1 B')
    expect(formatBytes(900)).toBe('900 B')
    expect(formatBytes(1023)).toBe('1023 B')
  })

  test('KB / MB / GB carry one decimal', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(4200)).toBe('4.1 KB')
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB')
    expect(formatBytes(2.25 * 1024 * 1024)).toBe('2.3 MB')
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3.0 GB')
  })

  test('never loses the unit when crossing thresholds', () => {
    // 1023.96 KB would print "1024.0 KB": rounding must promote the unit.
    expect(formatBytes(1023.96 * 1024)).toBe('1.0 MB')
    // Just below that, the unit is kept as-is (no premature promotion).
    expect(formatBytes(1023.6 * 1024)).toBe('1023.6 KB')
  })

  test('garbage in, safe label out', () => {
    expect(formatBytes(-5)).toBe('0 B')
    expect(formatBytes(Number.NaN)).toBe('0 B')
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe('0 B')
  })
})
