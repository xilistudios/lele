/**
 * Stream metrics (token generation speed) for the chat UI.
 *
 * The backend emits per-chunk metrics on every `message.stream` event (the
 * answer surface) AND on every `message.thinking` event (the reasoning surface,
 * metered separately so the rate is already live while the model thinks):
 * `tokens`, `elapsed_ms`, `tok_per_sec` and `estimated`. They describe the
 * CURRENT segment: a turn that uses tool-calls produces SEVERAL segments, each
 * with its own `message_id`, and the chip always reflects the segment that is
 * generating right now.
 *
 * This module owns three things:
 *  1. the parsed shape (`StreamMetrics`) and a tolerant parser for the raw WS
 *     payload (an older backend that omits the fields degrades to zeros);
 *  2. the retention policy — only a POSITIVE rate is stored, so the chip does
 *     not blink to "0.0 tok/s" on the first chunk of a segment (where
 *     `elapsed_ms` is 0 by definition and no rate can be computed yet) nor on a
 *     metrics-less payload; the previous figure is kept instead;
 *  3. locale-neutral formatting for the UI (1 decimal, always a `.`).
 *
 * The value is stored GLOBALLY, not per-session: `handleMessageStream` drops
 * any event whose session does not match the one on screen, so only the current
 * conversation can ever update it. It is cleared explicitly on a new send, a
 * session switch and a welcome/reconnect (see `useMessages` and
 * `handleWelcome`).
 */
import { useCallback, useState } from 'react'

export type StreamMetrics = {
  /** Tokens produced in the current segment. */
  tokens: number
  /** Milliseconds since the first content chunk of the segment. */
  elapsedMs: number
  /** Tokens per second over the segment so far. */
  tokPerSec: number
  /** True when the rate is a backend heuristic rather than a measurement. */
  estimated: boolean
}

/**
 * Extract the metrics fields from a `message.stream` or `message.thinking`
 * payload.
 *
 * Tolerant by design: a missing/non-numeric field falls back to `0` and a
 * missing `estimated` to `false`, so a legacy backend (or any event without
 * these fields) can never crash or produce NaN.
 */
export function parseStreamMetrics(data: Record<string, unknown>): StreamMetrics {
  const tokens = Number(data.tokens)
  const elapsedMs = Number(data.elapsed_ms)
  const tokPerSec = Number(data.tok_per_sec)
  return {
    tokens: Number.isFinite(tokens) ? tokens : 0,
    elapsedMs: Number.isFinite(elapsedMs) ? elapsedMs : 0,
    tokPerSec: Number.isFinite(tokPerSec) ? tokPerSec : 0,
    estimated: data.estimated === true,
  }
}

/**
 * Locale-neutral one-decimal format with a decimal point (e.g. `42.5`).
 * `toFixed` never emits a comma, so the reading is stable across locales.
 */
export function formatTokPerSec(rate: number): string {
  const safe = Number.isFinite(rate) && rate > 0 ? rate : 0
  return (Math.round(safe * 10) / 10).toFixed(1)
}

/** The chip is shown only for a meaningful, positive rate. */
export function isMetricsVisible(metrics: StreamMetrics | null): metrics is StreamMetrics {
  return metrics !== null && metrics.tokPerSec > 0
}

/**
 * Holds the last stream metrics for the session on screen and applies the
 * retention policy. `reportMetrics` is the setter wired into the event context;
 * `clearMetrics` drops everything (new send / session switch / reconnect).
 */
export function useStreamMetrics() {
  const [metrics, setMetrics] = useState<StreamMetrics | null>(null)

  const reportMetrics = useCallback((next: StreamMetrics) => {
    // A non-positive rate carries no information (first chunk of a segment,
    // where elapsed_ms is 0, or a metrics-less payload). Keep the last figure
    // rather than blanking the chip.
    if (next.tokPerSec > 0) setMetrics(next)
  }, [])

  const clearMetrics = useCallback(() => setMetrics(null), [])

  return { metrics, reportMetrics, clearMetrics }
}
