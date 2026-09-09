import { type CSSProperties, useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '../atoms/Badge'
import { IconButton } from '../atoms/IconButton'

/**
 * Temperature slider (spec §7.2 + §4.4).
 *
 * `value === undefined` is a first-class state: "not set, use the model's own
 * default". The thumb then sits at `modelDefault` (0.7 by default) at 60%
 * opacity with a dashed thumb, a "Model default" badge labels the readout, and
 * reset writes `undefined` back — NOT 0.7, because the semantics of reset are
 * "inherit", not "jump to the number".
 *
 * Deviation documented for PR: §4.4 says the reset button is visible when the
 * value differs from the default *or the field is dirty*. The §7.2 props API
 * carries no dirty flag, so the button is shown whenever a value is set
 * (`value !== undefined`) — the only case where it can actually do something.
 * Hiding it for an explicitly-set 0.7 would make that value impossible to
 * clear back to inherit.
 *
 * Cross-browser styling of the native `<input type="range">` (thumb/track)
 * lives in `styles/index.css` as `.lele-range`: the only CSS this feature adds
 * (§9), because Tailwind cannot reach `::-webkit-slider-thumb` without a plugin.
 */

type Props = {
  id: string
  /** undefined = inherit the model default. */
  value: number | undefined
  onChange: (value: number | undefined) => void
  min?: number
  max?: number
  step?: number
  /** Tick mark + semantic of the inherited value. Default 0.7. */
  modelDefault?: number
  disabled?: boolean
  ariaLabel?: string
}

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value))

/** 0.70 -> "0.7" · 1.00 -> "1" · 0.05 -> "0.05" (never a truncated ellipsis). */
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

  // The native input is the only element, so the filled portion of the track
  // is painted with a gradient stop at the current percentage.
  const trackStyle: CSSProperties = {
    background: `linear-gradient(to right, var(--color-interaction-primary) 0%, var(--color-interaction-primary) ${percent}%, var(--color-bg-tertiary) ${percent}%, var(--color-bg-tertiary) 100%)`,
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
          className="mt-1 flex justify-between text-[10px] text-text-muted"
          data-testid={`${id}-ticks`}
        >
          <span>{min}</span>
          <span title={notSetLabel}>{modelDefault}</span>
          <span>{max}</span>
        </div>

        <p id={helpId} className="mt-1.5 text-[11px] text-text-tertiary">
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
