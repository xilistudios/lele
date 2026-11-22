import { useTranslation } from 'react-i18next'
import { useStreamMetricsContext } from '../../contexts/AppLogicContext'
import { formatTokPerSec, isMetricsVisible } from '../../hooks/useStreamMetrics'

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
