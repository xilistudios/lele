import { type CSSProperties, useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '../atoms/Badge'
import { IconButton } from '../atoms/IconButton'

type Props = {
  id: string
  value: number | undefined
  onChange: (value: number | undefined) => void
  min?: number
  max?: number
  step?: number
  modelDefault?: number
  disabled?: boolean
  ariaLabel?: string
}

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value))

function formatTemperature(value: number): string {
  return String(Number(value.toFixed(2)))
}

export function TemperatureSlider({
  id,
  value,
  onChange,
  min = 0,
  max = 2,
  step = 0.05,
  modelDefault = 0.7,
  disabled = false,
  ariaLabel,
}: Props) {
  const { t } = useTranslation()
  const helpId = `temp-help-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`

  const isInherited = value === undefined || Number.isNaN(value)
  const shown = isInherited ? clamp(modelDefault, min, max) : clamp(value, min, max)
  const percent = max > min ? ((shown - min) / (max - min)) * 100 : 0

  const trackStyle: CSSProperties = {
    background: `linear-gradient(to right, rgb(var(--color-focus)) 0%, rgb(var(--color-focus)) ${percent}%, rgb(var(--color-bg-tertiary)) ${percent}%, rgb(var(--color-bg-tertiary)) 100%)`,
  }

  const notSetLabel = t('settings.agentPage.tempNotSet', { defaultValue: 'Model default' })
  const help = t('settings.agentPage.tempHelp', {
    default: modelDefault,
    defaultValue: '0 = deterministic · 2 = creative. Unset uses the model default ({{default}}).',
  })

  return (
    <div className="flex items-center gap-4">
      <div className="min-w-0 flex-1">
        <input
          id={id}
          aria-label={
            ariaLabel ?? t('settings.fields.agentTemperature', { defaultValue: 'Temperature' })
          }
          aria-valuemin={min}
          aria-valuemax={max}
          aria-valuenow={shown}
          aria-valuetext={
            isInherited ? `${formatTemperature(shown)} — ${notSetLabel}` : formatTemperature(shown)
          }
          aria-describedby={helpId}
          aria-disabled={disabled || undefined}
          className={`lele-range w-full ${isInherited ? 'lele-range--inherit' : ''}`}
          disabled={disabled}
          data-inherited={isInherited ? 'true' : 'false'}
          max={max}
          min={min}
          step={step}
          style={trackStyle}
          type="range"
          value={shown}
          onChange={(event) => {
            const parsed = Number.parseFloat(event.target.value)
            if (!Number.isNaN(parsed)) onChange(parsed)
          }}
        />

        <div
          className="mt-1 flex justify-between text-2xs text-text-muted"
          data-testid={`${id}-ticks`}
        >
          <span>{min}</span>
          <span title={notSetLabel}>{modelDefault}</span>
          <span>{max}</span>
        </div>

        <p id={helpId} className="mt-1.5 text-xs text-text-tertiary">
          {help}
        </p>
      </div>

      <span className="w-14 flex-none text-right font-mono text-sm text-text-primary">
        {formatTemperature(shown)}
      </span>

      {isInherited ? (
        <Badge className="flex-none" size="sm" variant="default">
          {notSetLabel}
        </Badge>
      ) : (
        <IconButton
          ariaLabel={t('settings.agentPage.tempReset', { defaultValue: 'Reset to model default' })}
          className="h-6 w-6 flex-none"
          disabled={disabled}
          title={t('settings.agentPage.tempReset', { defaultValue: 'Reset to model default' })}
          variant="ghost"
          onClick={() => onChange(undefined)}
        >
          <svg
            aria-hidden="true"
            className="h-3 w-3"
            fill="none"
            height="12"
            stroke="currentColor"
            strokeWidth="2"
            viewBox="0 0 24 24"
            width="12"
          >
            <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
            <path d="M3 3v5h5" />
          </svg>
        </IconButton>
      )}
    </div>
  )
}
