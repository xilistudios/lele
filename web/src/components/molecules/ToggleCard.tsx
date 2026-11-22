import { type MouseEvent, type ReactNode, useId } from 'react'

type Props = {
  id: string
  checked: boolean
  onChange: (checked: boolean) => void
  title: string
  titleMono?: boolean
  description?: string
  descriptionLines?: 1 | 2
  badge?: ReactNode
  actions?: ReactNode
  warning?: string
  disabled?: boolean
  size?: 'sm' | 'md'
}

const CARD_BASE_CLS =
  'relative flex cursor-pointer gap-2.5 rounded-lg border transition-colors duration-fast focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-focus'

const CARD_ON_CLS = 'border-focus/40 bg-accent-subtle'
const CARD_OFF_CLS = 'border-border bg-background-secondary hover:border-border-strong'
const CARD_DISABLED_CLS = 'opacity-40 cursor-not-allowed'

const SIZE_CLS: Record<'sm' | 'md', string> = {
  sm: 'p-3',
  md: 'p-3.5',
}

const CHECK_SIZE_CLS: Record<'sm' | 'md', string> = {
  sm: 'h-4 w-4',
  md: 'h-[18px] w-[18px]',
}

const CHECK_BASE_CLS = 'flex flex-none items-center justify-center rounded-md border-2'
const CHECK_ON_CLS = 'border-focus bg-focus text-text-on-accent'
const CHECK_OFF_CLS = 'border-border-strong bg-background-primary'

function CardActions({ children }: { children: ReactNode }) {
  const swallow = (event: MouseEvent) => {
    event.preventDefault()
    event.stopPropagation()
  }
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: non-interactive click canceller, see above
    <span className="flex flex-none items-center gap-1" onClick={swallow} onMouseDown={swallow}>
      {children}
    </span>
  )
}

export function ToggleCard({
  id,
  checked,
  onChange,
  title,
  titleMono = true,
  description,
  descriptionLines = 1,
  badge,
  actions,
  warning,
  disabled = false,
  size = 'md',
}: Props) {
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
          {actions ? <CardActions>{actions}</CardActions> : null}
        </span>

        {description ? (
          <span
            className={`text-text-tertiary ${descriptionLines === 2 ? 'line-clamp-2 text-xs' : 'line-clamp-1 text-xs'}`}
          >
            {description}
          </span>
        ) : null}

        {warning ? (
          <span id={warningId} className="text-xs text-state-warning">
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
