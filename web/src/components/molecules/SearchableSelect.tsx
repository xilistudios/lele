import { useEffect, useMemo, useRef, useState } from 'react'

type Option = {
  value: string
  label: string
}

type OptionGroup = {
  label: string
  options: Option[]
}

type Props = {
  ariaLabel: string
  buttonLabel: string
  disabled?: boolean
  emptyLabel: string
  groups?: OptionGroup[]
  onChange: (value: string) => void
  options?: Option[]
  placeholder: string
  searchAriaLabel: string
  searchPlaceholder: string
  value: string
}

const ANIMATION_MS = 200

export function SearchableSelect({
  ariaLabel,
  buttonLabel,
  disabled = false,
  emptyLabel,
  groups,
  onChange,
  options = [],
  placeholder,
  searchAriaLabel,
  searchPlaceholder,
  value,
}: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const closeTimerRef = useRef<number | null>(null)
  const [isMounted, setIsMounted] = useState(false)
  const [isOpen, setIsOpen] = useState(false)
  const [query, setQuery] = useState('')

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

  useEffect(() => {
    if (!isMounted) return undefined

    const frame = window.requestAnimationFrame(() => {
      setIsOpen(true)
      window.requestAnimationFrame(() => searchRef.current?.focus())
    })

    return () => window.cancelAnimationFrame(frame)
  }, [isMounted])

  useEffect(() => {
    if (!isMounted) return undefined

    const handlePointerDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setIsOpen(false)
        if (closeTimerRef.current) {
          window.clearTimeout(closeTimerRef.current)
        }
        closeTimerRef.current = window.setTimeout(() => {
          setIsMounted(false)
          closeTimerRef.current = null
        }, ANIMATION_MS)
      }
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsOpen(false)
        if (closeTimerRef.current) {
          window.clearTimeout(closeTimerRef.current)
        }
        closeTimerRef.current = window.setTimeout(() => {
          setIsMounted(false)
          closeTimerRef.current = null
        }, ANIMATION_MS)
      }
    }

    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)

    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [isMounted])

  useEffect(
    () => () => {
      if (closeTimerRef.current) {
        window.clearTimeout(closeTimerRef.current)
      }
    },
    [],
  )

  const open = () => {
    if (disabled) return
    setQuery('')
    setIsMounted(true)
  }

  const close = () => {
    setIsOpen(false)
    if (closeTimerRef.current) {
      window.clearTimeout(closeTimerRef.current)
    }
    closeTimerRef.current = window.setTimeout(() => {
      setIsMounted(false)
      closeTimerRef.current = null
    }, ANIMATION_MS)
  }

  const handleSelect = (nextValue: string) => {
    onChange(nextValue)
    close()
  }

  return (
    <div ref={rootRef} className="relative">
      <button
        aria-label={ariaLabel}
        className="flex min-w-0 items-center gap-2 rounded-md border border-[#2f2f2f] bg-[#1a1a1a] px-3 py-2 text-sm text-[#ddd] transition-all duration-200 hover:border-[#4a4a4a] hover:bg-[#202020] hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
        disabled={disabled}
        type="button"
        onClick={() => (isMounted ? close() : open())}
      >
        <span className="min-w-0 truncate text-sm text-[#888]">{buttonLabel}</span>
        <span className="min-w-0 flex-1 truncate text-left text-sm font-medium text-white">
          {selectedOption?.label ?? placeholder}
        </span>
        <svg
          aria-hidden="true"
          className={`h-3.5 w-3.5 flex-none transition-transform duration-200 ${isMounted ? 'rotate-180' : ''}`}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        >
          <polyline points="6 9 12 15 18 9" />
        </svg>
      </button>

      {isMounted ? (
        <div
          className={`absolute bottom-full left-0 z-30 mb-2 w-[min(24rem,calc(100vw-3rem))] rounded-xl border border-[#3a3a3a] bg-[#1d1d1d] shadow-2xl transition-all duration-200 ease-out ${
            isOpen
              ? 'translate-y-0 scale-100 opacity-100'
              : 'pointer-events-none translate-y-2 scale-95 opacity-0'
          }`}
        >
          <div className="border-b border-[#2f2f2f] p-3">
            <div className="flex items-center gap-2 rounded-lg border border-[#2f2f2f] bg-[#141414] px-3 py-2 text-sm text-[#bbb] transition-colors focus-within:border-[#5a5a5a]">
              <svg
                aria-hidden="true"
                className="h-4 w-4 flex-none text-[#666]"
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
                className="w-full bg-transparent text-sm text-white outline-none placeholder:text-[#555]"
                placeholder={searchPlaceholder}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
          </div>

          <div className="max-h-60 overflow-y-auto p-2">
            {hasResults ? (
              <div className="space-y-2">
                {filteredGroups.map((group) => (
                  <div key={group.label || 'default'} className="space-y-1">
                    {group.label ? (
                      <p className="px-2 text-[10px] uppercase tracking-[0.2em] text-[#666]">
                        {group.label}
                      </p>
                    ) : null}
                    <div className="space-y-1">
                      {group.options.map((option) => {
                        const active = option.value === value

                        return (
                          <button
                            key={option.value}
                            className={`flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm transition-all duration-150 ${
                              active
                                ? 'bg-[#2f2f2f] text-white'
                                : 'text-[#c9c9c9] hover:bg-[#242424] hover:text-white'
                            }`}
                            type="button"
                            onClick={() => handleSelect(option.value)}
                          >
                            <span className="truncate">{option.label}</span>
                            {active ? (
                              <span className="ml-3 text-xs text-emerald-400">●</span>
                            ) : null}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <p className="px-3 py-4 text-sm text-[#666]">{emptyLabel}</p>
            )}
          </div>
        </div>
      ) : null}
    </div>
  )
}
