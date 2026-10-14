import { useTranslation } from 'react-i18next'
import { useStreamMetricsContext } from '../../contexts/AppLogicContext'
import { formatTokPerSec, isMetricsVisible } from '../../hooks/useStreamMetrics'

/**
 * Discrete chip showing the model's token generation speed (e.g. `42.5 tok/s`)
 * next to the chat input.
 *
 * It reads from `StreamMetricsContext` (never the cold app context) so a
 * per-chunk rate update re-renders ONLY this node — the composer, its draft
 * state and the rest of the toolbar are untouched.
 *
 * Lifecycle: visible while the backend reports a rate (live during streaming)
 * and KEPT after a segment finishes, because the retention policy in
 * `useStreamMetrics` remembers the last positive value. It is hidden whenever
 * no valid rate exists — a chat just opened, a restored/reconnected turn (the
 * backend re-emits no metrics), an error, or a segment whose first chunk has no
 * rate yet. `0.0 tok/s` is never rendered.
 */
export function StreamMetricsChip({ className }: { className?: string }) {
  const { t } = useTranslation()
  const metrics = useStreamMetricsContext()

  if (!isMetricsVisible(metrics)) return null

  const label = t('chat.tokensPerSecond', { rate: formatTokPerSec(metrics.tokPerSec) })

  return (
    <span
      className={`inline-flex items-center whitespace-nowrap font-mono text-2xs text-text-tertiary ${
        className ?? ''
      }`}
      title={t('chat.tokensPerSecondTitle')}
      aria-label={t('chat.tokensPerSecondTitle')}
    >
      {label}
    </span>
  )
}
