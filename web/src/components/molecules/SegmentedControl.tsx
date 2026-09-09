import { type KeyboardEvent, type ReactNode, useRef } from 'react'

/**
 * Segmented control (spec §7.1) — 2..5 mutually exclusive options where every
 * option must be visible at once (thinking level, tools mode).
 *
 * Deliberately not a `<select>`: the active value has to be readable without
 * interacting. Implemented as a WAI-ARIA radio group with roving tabindex:
 * ←/→ move the selection (and focus) and activate immediately, Home/End jump to
 * the ends, Enter/Space activate the focused option.
 */

export type SegmentOption<T extends string> = {
  value: T
  label: string
  icon?: ReactNode
}

type Props<T extends string> = {
  id: string
  options: SegmentOption<T>[]
  /** Current value. `''` is allowed and means "inherit / not set". */
  value: T
  onChange: (value: T) => void
  disabled?: boolean
  /** sm: h-7 text-xs · md (default): h-9 text-sm */
  size?: 'sm' | 'md'
  /** Stretch every segment to the container width. */
  fullWidth?: boolean
  /** Required when no <label htmlFor={id}> is associated. */
  ariaLabel?: string
}

const CONTAINER_CLS =
  'inline-flex items-center rounded-lg border border-border bg-background-tertiary p-0.5 gap-0.5'

const ITEM_BASE_CLS =
  'flex-1 whitespace-nowrap rounded-md px-3 py-1 font-medium transition-colors duration-fast'

const ITEM_ON_CLS = 'bg-background-secondary text-text-primary shadow-card'
const ITEM_OFF_CLS = 'text-text-tertiary hover:text-text-primary'

const SIZE_CLS: Record<'sm' | 'md', string> = {
  sm: 'h-7 text-xs',
  md: 'h-9 text-sm',
}

export function SegmentedControl<T extends string>({
  id,
  options,
  value,
  onChange,
  disabled = false,
  size = 'md',
  fullWidth = false,
  ariaLabel,
}: Props<T>) {
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([])

  const selectedIndex = Math.max(
    0,
    options.findIndex((option) => option.value === value),
  )

  const activate = (index: number) => {
    const option = options[index]
    if (!option || disabled) return
    onChange(option.value)
  }

  const focusItem = (index: number) => {
    const target = itemRefs.current[index]
    target?.focus()
    // Radio groups move the selection together with the focus (§7.1: arrows
    // "navegan y activan"), so focusing through the keyboard also commits.
    activate(index)
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    if (disabled) return
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        event.preventDefault()
        focusItem((index + 1) % options.length)
        break
      case 'ArrowLeft':
      case 'ArrowUp':
        event.preventDefault()
        focusItem((index - 1 + options.length) % options.length)
        break
      case 'Home':
        event.preventDefault()
        focusItem(0)
        break
      case 'End':
        event.preventDefault()
        focusItem(options.length - 1)
        break
      case 'Enter':
      case ' ':
        event.preventDefault()
        activate(index)
        break
      default:
        break
    }
  }

  return (
    <div
      id={id}
      role="radiogroup"
      aria-label={ariaLabel}
      aria-disabled={disabled || undefined}
      className={`${CONTAINER_CLS} ${fullWidth ? 'flex w-full' : ''}`}
    >
      {options.map((option, index) => {
        const checked = option.value === value
        return (
          <button
            key={option.value}
            ref={(element) => {
              itemRefs.current[index] = element
            }}
            type="button"
            // biome-ignore lint/a11y/useSemanticElements: spec §7.1 requires a radiogroup of visible segments; native <input type=radio> cannot render the raised-surface pill styling and would submit with the surrounding form
            role="radio"
            aria-checked={checked}
            // Roving tabindex: only the selected option is in the tab order.
            tabIndex={index === selectedIndex ? 0 : -1}
            disabled={disabled}
            title={option.label}
            className={`${ITEM_BASE_CLS} ${SIZE_CLS[size]} ${checked ? ITEM_ON_CLS : ITEM_OFF_CLS} disabled:opacity-40`}
            onClick={() => activate(index)}
            onKeyDown={(event) => handleKeyDown(event, index)}
          >
            {option.icon ? (
              <span aria-hidden="true" className="mr-1.5 inline-flex align-middle opacity-80">
                {option.icon}
              </span>
            ) : null}
            {option.label}
          </button>
        )
      })}
    </div>
  )
}
