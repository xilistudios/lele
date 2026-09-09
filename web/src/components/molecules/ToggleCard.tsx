import { type ReactNode, useId } from 'react'

/**
 * ToggleCard (spec §7.3) — shared base of the Skills and Tools grids.
 *
 * A whole card is the click target: the root element is a `<label>` wrapping a
 * real `sr-only` checkbox, so it is keyboard focusable, announces its state
 * natively ("read_file, checkbox, checked"), toggles with Space, and needs no
 * custom ARIA. Focus is shown on the card via `:focus-within`.
 *
 * - off: `border-border bg-background-secondary hover:border-border-strong`
 * - on:  `border-interaction-primary/40 bg-accent-subtle` + filled check
 * - size `sm` (p-3, 16px check) = tools · `md` (p-3.5, 18px check) = skills
 */

type Props = {
  id: string
  checked: boolean
  onChange: (checked: boolean) => void
  title: string
  /** Monospace title (tool/skill identifiers). Default true. */
  titleMono?: boolean
  description?: string
  /** 1 = tools (compact) · 2 = skills. */
  descriptionLines?: 1 | 2
  /** Source / "not installed" / "disabled globally" badge slot. */
  badge?: ReactNode
  /** Inline hint, wired to the input through aria-describedby. */
  warning?: string
  disabled?: boolean
  size?: 'sm' | 'md'
}

const CARD_BASE_CLS =
  'relative flex cursor-pointer gap-2.5 rounded-lg border transition-colors duration-fast focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-interaction-primary'

const CARD_ON_CLS = 'border-interaction-primary/40 bg-accent-subtle'
const CARD_OFF_CLS = 'border-border bg-background-secondary hover:border-border-strong'
const CARD_DISABLED_CLS = 'opacity-40 cursor-not-allowed'

const SIZE_CLS: Record<'sm' | 'md', string> = {
  sm: 'p-3',
  md: 'p-3.5',
}

/** Check box: 16px in `sm`, 18px in `md`. */
const CHECK_SIZE_CLS: Record<'sm' | 'md', string> = {
  sm: 'h-4 w-4',
  md: 'h-[18px] w-[18px]',
}

const CHECK_BASE_CLS = 'flex flex-none items-center justify-center rounded-md border-2'
const CHECK_ON_CLS = 'border-interaction-primary bg-interaction-primary text-text-on-accent'
const CHECK_OFF_CLS = 'border-border-strong bg-background-primary'

export function ToggleCard({
  id,
  checked,
  onChange,
  title,
  titleMono = true,
  description,
  descriptionLines = 1,
  badge,
  warning,
  disabled = false,
  size = 'md',
}: Props) {
  // useId() contains ':' (valid in HTML ids, awkward in CSS selectors), so it is
  // sanitized before being used as an attribute value.
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const inputId = `${id}-toggle-${uid}`
  const warningId = warning ? `${id}-warning-${uid}` : undefined

  return (
    <label
      htmlFor={inputId}
      className={`${CARD_BASE_CLS} ${checked ? CARD_ON_CLS : CARD_OFF_CLS} ${SIZE_CLS[size]} ${disabled ? CARD_DISABLED_CLS : ''}`}
    >
      <input
        id={inputId}
        type="checkbox"
        className="sr-only"
        checked={checked}
        disabled={disabled}
        aria-describedby={warningId}
        // Guarded (jsdom toggles disabled inputs, real browsers do not): a
        // disabled card must never report a change.
        onChange={(event) => {
          if (disabled) return
          onChange(event.target.checked)
        }}
      />

      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex min-w-0 items-center gap-2">
          <span
            title={title}
            className={`min-w-0 flex-1 truncate text-text-primary ${
              titleMono ? 'font-mono text-xs font-medium' : 'text-sm font-medium'
            }`}
          >
            {title}
          </span>
          {badge ? <span className="flex-none">{badge}</span> : null}
        </span>

        {description ? (
          <span
            className={`text-text-tertiary ${descriptionLines === 2 ? 'line-clamp-2 text-xs' : 'line-clamp-1 text-[11px]'}`}
          >
            {description}
          </span>
        ) : null}

        {warning ? (
          <span id={warningId} className="text-[11px] text-state-warning">
            {warning}
          </span>
        ) : null}
      </span>

      <span
        aria-hidden="true"
        className={`${CHECK_BASE_CLS} ${CHECK_SIZE_CLS[size]} ${checked ? CHECK_ON_CLS : CHECK_OFF_CLS}`}
      >
        {checked ? (
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="3"
            aria-hidden="true"
          >
            <polyline points="20 6 9 17 4 12" />
          </svg>
        ) : null}
      </span>
    </label>
  )
}
