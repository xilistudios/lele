import { useCallback, useState } from 'react'

export type StreamMetrics = {
  tokens: number
  elapsedMs: number
  tokPerSec: number
  estimated: boolean
}

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

export function formatTokPerSec(rate: number): string {
  const safe = Number.isFinite(rate) && rate > 0 ? rate : 0
  return (Math.round(safe * 10) / 10).toFixed(1)
}

export function isMetricsVisible(metrics: StreamMetrics | null): metrics is StreamMetrics {
  return metrics !== null && metrics.tokPerSec > 0
}

export function useStreamMetrics() {
  const [metrics, setMetrics] = useState<StreamMetrics | null>(null)

  const reportMetrics = useCallback((next: StreamMetrics) => {
    if (next.tokPerSec > 0) setMetrics(next)
  }, [])

  const clearMetrics = useCallback(() => setMetrics(null), [])

  return { metrics, reportMetrics, clearMetrics }
}
