/**
 * Unit tests for the stream metrics module.
 *
 * Covers the three responsibilities extracted there: tolerant parsing of the
 * `message.stream` payload, locale-neutral formatting (1 decimal, dot), the
 * visibility rule, and the hook's retention policy (positive rates are kept
 * across a zero-rate chunk; the first chunk of a segment and an explicit clear
 * leave the chip empty).
 */
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, renderHook } from '@testing-library/react'
import {
  formatTokPerSec,
  isMetricsVisible,
  parseStreamMetrics,
  useStreamMetrics,
} from './useStreamMetrics'

afterEach(cleanup)

describe('parseStreamMetrics', () => {
  test('reads all four fields from a full payload', () => {
    expect(
      parseStreamMetrics({
        tokens: 120,
        elapsed_ms: 1350,
        tok_per_sec: 88.33333333333333,
        estimated: true,
      }),
    ).toEqual({ tokens: 120, elapsedMs: 1350, tokPerSec: 88.33333333333333, estimated: true })
  })

  test('legacy payload without the fields degrades to safe defaults', () => {
    expect(parseStreamMetrics({ message_id: 'm1', chunk: 'hi', done: false })).toEqual({
      tokens: 0,
      elapsedMs: 0,
      tokPerSec: 0,
      estimated: false,
    })
  })

  test('non-numeric / missing values never produce NaN', () => {
    const m = parseStreamMetrics({ tokens: 'x', elapsed_ms: null, tok_per_sec: undefined })
    expect(m).toEqual({ tokens: 0, elapsedMs: 0, tokPerSec: 0, estimated: false })
    expect(Number.isNaN(m.tokPerSec)).toBe(false)
  })

  test('estimated only true for an explicit boolean true', () => {
    expect(parseStreamMetrics({ estimated: true }).estimated).toBe(true)
    expect(parseStreamMetrics({ estimated: false }).estimated).toBe(false)
    expect(parseStreamMetrics({ estimated: 'yes' }).estimated).toBe(false)
  })
})

describe('formatTokPerSec', () => {
  test('one decimal, locale-neutral dot', () => {
    expect(formatTokPerSec(50)).toBe('50.0')
    expect(formatTokPerSec(88.33333333333333)).toBe('88.3')
    expect(formatTokPerSec(0.06)).toBe('0.1')
  })

  test('non-positive / non-finite falls back to 0.0 (never rendered)', () => {
    expect(formatTokPerSec(0)).toBe('0.0')
    expect(formatTokPerSec(-3)).toBe('0.0')
    expect(formatTokPerSec(Number.NaN)).toBe('0.0')
    expect(formatTokPerSec(Number.POSITIVE_INFINITY)).toBe('0.0')
  })
})

describe('isMetricsVisible', () => {
  test('only a positive rate is visible', () => {
    expect(isMetricsVisible(null)).toBe(false)
    expect(isMetricsVisible({ tokens: 0, elapsedMs: 0, tokPerSec: 0, estimated: false })).toBe(
      false,
    )
    expect(isMetricsVisible({ tokens: 5, elapsedMs: 10, tokPerSec: 0, estimated: false })).toBe(
      false,
    )
    expect(isMetricsVisible({ tokens: 50, elapsedMs: 1000, tokPerSec: 50, estimated: false })).toBe(
      true,
    )
  })
})

describe('useStreamMetrics', () => {
  test('starts empty', () => {
    const { result } = renderHook(() => useStreamMetrics())
    expect(result.current.metrics).toBeNull()
  })

  test('stores a positive report and retains it across a zero-rate (done) chunk', () => {
    const { result } = renderHook(() => useStreamMetrics())
    act(() =>
      result.current.reportMetrics({
        tokens: 50,
        elapsedMs: 1000,
        tokPerSec: 50,
        estimated: false,
      }),
    )
    expect(result.current.metrics?.tokPerSec).toBe(50)

    // Terminal / metrics-less chunk must NOT blank the retained final figure.
    act(() =>
      result.current.reportMetrics({ tokens: 0, elapsedMs: 0, tokPerSec: 0, estimated: false }),
    )
    expect(result.current.metrics?.tokPerSec).toBe(50)
  })

  test('ignores the first chunk of a segment (elapsed 0 → tokPerSec 0)', () => {
    const { result } = renderHook(() => useStreamMetrics())
    act(() =>
      result.current.reportMetrics({ tokens: 1, elapsedMs: 0, tokPerSec: 0, estimated: false }),
    )
    expect(result.current.metrics).toBeNull()
  })

  test('clearMetrics drops the figure (session switch / new send)', () => {
    const { result } = renderHook(() => useStreamMetrics())
    act(() =>
      result.current.reportMetrics({ tokens: 10, elapsedMs: 500, tokPerSec: 20, estimated: true }),
    )
    expect(result.current.metrics).not.toBeNull()
    act(() => result.current.clearMetrics())
    expect(result.current.metrics).toBeNull()
  })
})
