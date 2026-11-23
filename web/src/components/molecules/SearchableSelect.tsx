import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { shortModelName } from '../../lib/modelName'

type Option = {
  value: string
  label: string
}

type OptionGroup = {
  label: string
  options: Option[]
}

type Direction = 'up' | 'down'

type Shape = 'default' | 'pill'

type Props = {
  ariaLabel: string
  buttonLabel: string
  className?: string
  direction?: Direction
  disabled?: boolean
  emptyLabel: string
  groups?: OptionGroup[]
  onChange: (value: string) => void
  options?: Option[]
  placeholder: string
  searchAriaLabel: string
  searchPlaceholder: string
  shape?: Shape
  value: string
}

const ENTER_MS = 200
const EXIT_MS = 150
const PANEL_HEIGHT = 256

const ENTER_EASING = 'cubic-bezier(0.16, 1, 0.3, 1)'
const EXIT_EASING = 'cubic-bezier(0.4, 0, 1, 1)'

const PANEL_BASE =
  'absolute left-0 z-[9999] w-[min(28rem,calc(100vw-4rem))] rounded-xl border border-border bg-background-secondary shadow-xl transition-[opacity,transform] motion-reduce:transition-none'

const triggerShapeStyles: Record<Shape, string> = {
  default: 'rounded-md px-2 py-1.5 sm:px-3 sm:py-2',
  pill: 'rounded-full py-1.5 pl-3 pr-2.5 sm:py-2 sm:pl-3.5 sm:pr-3',
}

const formatDisplayValue = shortModelName

export function SearchableSelect({
  ariaLabel,
  buttonLabel,
  className,
  direction = 'up',
  disabled = false,
  emptyLabel,
  groups,
  onChange,
  options = [],
  placeholder,
  searchAriaLabel,
  searchPlaceholder,
  shape = 'default',
  value,
}: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const enterFrameRef = useRef<number | null>(null)
  const exitTimerRef = useRef<number | null>(null)
  const [isMounted, setIsMounted] = useState(false)
  const [isShown, setIsShown] = useState(false)
  const [query, setQuery] = useState('')
  const [isOpenUp, setIsOpenUp] = useState(false)
  const [listHeight, setListHeight] = useState<number | null>(null)

  const allOptions = useMemo(() => {
    return groups ? groups.flatMap((group) => group.options) : options
  }, [groups, options])

  const selectedOption = useMemo(
    () => allOptions.find((option) => option.value === value),
    [allOptions, value],
  )

  const filteredGroups = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()

    if (groups) {
      return groups
        .map((group) => ({
          label: group.label,
          options: group.options.filter((option) => {
            if (!normalizedQuery) return true
            return (
              option.label.toLowerCase().includes(normalizedQuery) ||
              option.value.toLowerCase().includes(normalizedQuery)
            )
          }),
        }))
        .filter((group) => group.options.length > 0)
    }

    return [
      {
        label: '',
        options: options.filter((option) => {
          if (!normalizedQuery) return true
          return (
            option.label.toLowerCase().includes(normalizedQuery) ||
            option.value.toLowerCase().includes(normalizedQuery)
          )
        }),
      },
    ].filter((group) => group.options.length > 0)
  }, [groups, options, query])

  const hasResults = filteredGroups.length > 0

  const resolveOpenUp = useCallback((): boolean => {
    if (!rootRef.current) return false
    const rect = rootRef.current.getBoundingClientRect()
    const viewportHeight = window.visualViewport?.height ?? window.innerHeight
    const spaceBelow = viewportHeight - rect.bottom

    return spaceBelow < PANEL_HEIGHT + 20
  }, [])

  const cancelEnterFrame = useCallback(() => {
    if (enterFrameRef.current === null) return
    window.cancelAnimationFrame(enterFrameRef.current)
    enterFrameRef.current = null
  }, [])

  const cancelExitTimer = useCallback(() => {
    if (exitTimerRef.current === null) return
    window.clearTimeout(exitTimerRef.current)
    exitTimerRef.current = null
  }, [])

  const close = useCallback(() => {
    cancelEnterFrame()
    cancelExitTimer()
    setIsShown(false)
    exitTimerRef.current = window.setTimeout(() => {
      exitTimerRef.current = null
      setIsMounted(false)
    }, EXIT_MS)
  }, [cancelEnterFrame, cancelExitTimer])

  const open = useCallback(() => {
    if (disabled) return
    cancelEnterFrame()
    cancelExitTimer()
    setQuery('')
    if (!isMounted) setListHeight(null)
    setIsOpenUp(resolveOpenUp())
    setIsMounted(true)
    setIsShown(false)
    enterFrameRef.current = window.requestAnimationFrame(() => {
      enterFrameRef.current = window.requestAnimationFrame(() => {
        enterFrameRef.current = null
        setIsShown(true)
        searchRef.current?.focus()
      })
    })
  }, [cancelEnterFrame, cancelExitTimer, disabled, isMounted, resolveOpenUp])

  useEffect(() => {
    if (!isMounted) return undefined

    const handlePointerDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) close()
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close()
    }

    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)

    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [isMounted, close])

  useEffect(
    () => () => {
      cancelEnterFrame()
      cancelExitTimer()
    },
    [cancelEnterFrame, cancelExitTimer],
  )

  useEffect(() => {
    if (!isMounted) return undefined

    const handleResize = () => setIsOpenUp(resolveOpenUp())

    const visualViewport = window.visualViewport
    window.addEventListener('resize', handleResize)
    visualViewport?.addEventListener('resize', handleResize)
    return () => {
      window.removeEventListener('resize', handleResize)
      visualViewport?.removeEventListener('resize', handleResize)
    }
  }, [isMounted, resolveOpenUp])

  useEffect(() => {
    const element = listRef.current
    if (!isMounted || !element || typeof ResizeObserver === 'undefined') return undefined

    const observer = new ResizeObserver(() => setListHeight(element.offsetHeight))
    observer.observe(element)

    return () => observer.disconnect()
  }, [isMounted])

  const handleSelect = (nextValue: string) => {
    onChange(nextValue)
    close()
  }

  const motionStyle = {
    transitionDuration: `${isShown ? ENTER_MS : EXIT_MS}ms`,
    transitionTimingFunction: isShown ? ENTER_EASING : EXIT_EASING,
  }

  const placement = isOpenUp
    ? 'bottom-full mb-2 origin-bottom-left'
    : 'top-full mt-2 origin-top-left'

  const visibility = isShown
    ? 'opacity-100 scale-100 translate-y-0'
    : 'pointer-events-none opacity-0 scale-[0.97] translate-y-2'

  return (
    <div ref={rootRef} className={`relative ${className ?? ''}`}>
      <button
        aria-label={ariaLabel}
        className={`flex w-full min-w-0 items-center gap-1.5 border border-border bg-background-primary text-xs text-text-primary transition-all duration-fast hover:border-border hover:bg-background-secondary hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-40 sm:gap-2 sm:text-sm ${triggerShapeStyles[shape]}`}
        disabled={disabled}
        type="button"
        onClick={() => (isShown ? close() : open())}
      >
        <span className="hidden sm:inline min-w-0 truncate text-xs sm:text-sm text-text-secondary">
          {buttonLabel}
        </span>
        <span className="min-w-0 flex-1 truncate text-left text-xs sm:text-sm font-medium text-text-primary">
          {formatDisplayValue(selectedOption?.label ?? placeholder)}
        </span>
        <svg
          aria-hidden="true"
          className={`h-3 w-3 sm:h-3.5 sm:w-3.5 flex-none transition-transform duration-normal ease-out motion-reduce:transition-none ${isMounted ? 'rotate-180' : ''}`}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        >
          <polyline points={direction === 'down' ? '6 15 12 9 18 15' : '6 9 12 15 18 9'} />
        </svg>
      </button>

      {isMounted ? (
        <div
          data-state={isShown ? 'open' : 'closed'}
          className={`${PANEL_BASE} ${placement} ${visibility}`}
          style={motionStyle}
        >
          <div className="border-b border-border p-3">
            <div className="flex items-center gap-2 rounded-lg border border-border bg-background-tertiary px-3 py-2 text-sm text-text-secondary transition-colors focus-within:border-focus">
              <svg
                aria-hidden="true"
                className="h-4 w-4 flex-none text-text-tertiary"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <circle cx="11" cy="11" r="7" />
                <path d="m21 21-4.3-4.3" />
              </svg>
              <input
                ref={searchRef}
                aria-label={searchAriaLabel}
                className="w-full bg-transparent text-sm text-text-primary outline-none placeholder:text-text-tertiary"
                placeholder={searchPlaceholder}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
          </div>

          <div
            className="overflow-hidden transition-[height] ease-out motion-reduce:transition-none"
            style={{ height: listHeight ?? undefined, transitionDuration: `${ENTER_MS}ms` }}
          >
            <div ref={listRef} className="max-h-64 overflow-y-auto p-2">
              {hasResults ? (
                <div className="space-y-2">
                  {filteredGroups.map((group) => (
                    <div key={group.label || 'default'} className="space-y-1">
                      {group.label ? (
                        <p className="px-2 text-2xs uppercase tracking-wider text-text-tertiary">
                          {group.label}
                        </p>
                      ) : null}
                      <div className="space-y-1">
                        {group.options.map((option) => {
                          const active = option.value === value

                          return (
                            <button
                              key={option.value}
                              className={`flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-left text-sm transition-all duration-fast ${
                                active
                                  ? 'bg-surface-selected text-accent-text border border-brand-rosa/30'
                                  : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
                              }`}
                              type="button"
                              onClick={() => handleSelect(option.value)}
                            >
                              <span className="truncate text-xs leading-relaxed">
                                {option.label}
                              </span>
                              {active ? (
                                <span className="ml-3 text-xs text-state-success">●</span>
                              ) : null}
                            </button>
                          )
                        })}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="px-3 py-4 text-sm text-text-tertiary">{emptyLabel}</p>
              )}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
