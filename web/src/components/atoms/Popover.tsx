import {
  type ReactNode,
  cloneElement,
  isValidElement,
  useEffect,
  useLayoutEffect,
  useState,
} from 'react'
import { usePopoverPosition } from '../../hooks/usePopoverPosition'

type Props = {
  trigger: ReactNode
  children: ReactNode
  popoverWidth?: number
  popoverHeight?: number
  block?: boolean
  tooltip?: ReactNode
}

const DEFAULT_POPOVER_WIDTH = 180
const DEFAULT_POPOVER_HEIGHT = 100

const ORIGIN_MAP = {
  below: { 'right-align': 'origin-top-right', 'left-align': 'origin-top-left' },
  above: { 'right-align': 'origin-bottom-right', 'left-align': 'origin-bottom-left' },
} as const

const TOOLTIP_HEIGHT = 30
const TOOLTIP_MIN_WIDTH = 80

type TooltipPosition = 'below' | 'above' | 'right'

export function Popover({
  trigger,
  children,
  popoverWidth = DEFAULT_POPOVER_WIDTH,
  popoverHeight = DEFAULT_POPOVER_HEIGHT,
  block = false,
  tooltip,
}: Props) {
  const [isOpen, setIsOpen] = useState(false)
  const [tooltipPosition, setTooltipPosition] = useState<TooltipPosition>('right')
  const { position, ref } = usePopoverPosition({
    isOpen,
    popoverWidth,
    popoverHeight,
  })

  useLayoutEffect(() => {
    if (!ref.current || isOpen || !tooltip) return
    const rect = ref.current.getBoundingClientRect()
    const spaceBelow = window.innerHeight - rect.bottom
    const spaceAbove = rect.top
    const spaceRight = window.innerWidth - rect.right

    const isNearLeftEdge = rect.left < TOOLTIP_MIN_WIDTH + 8

    if (isNearLeftEdge && spaceRight >= TOOLTIP_MIN_WIDTH + 8) {
      setTooltipPosition('right')
    } else if (spaceBelow >= TOOLTIP_HEIGHT + 8) {
      setTooltipPosition('below')
    } else if (spaceAbove >= TOOLTIP_HEIGHT + 8) {
      setTooltipPosition('above')
    } else {
      setTooltipPosition('right')
    }
  }, [isOpen, ref, tooltip])

  useEffect(() => {
    if (!isOpen) return

    const onClickOutside = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setIsOpen(false)
      }
    }

    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [isOpen, ref])

  const origin = ORIGIN_MAP[position.vertical][position.horizontal]
  const verticalClass = position.vertical === 'below' ? 'top-full mt-1' : 'bottom-full mb-1'
  const horizontalClass = position.horizontal === 'right-align' ? 'right-0' : 'left-0'

  const triggerWithOnClick = isValidElement(trigger)
    ? cloneElement(
        trigger as React.ReactElement<{
          onClick?: () => void
          onKeyDown?: (e: React.KeyboardEvent) => void
        }>,
        {
          onClick: () => setIsOpen((prev) => !prev),
          onKeyDown: (e: React.KeyboardEvent) => {
            if (e.key === 'Enter' || e.key === ' ') {
              setIsOpen((prev) => !prev)
            }
          },
        },
      )
    : trigger

  return (
    <div
      ref={ref}
      className={`relative ${!isOpen ? 'group' : ''} ${block ? 'block w-full' : 'inline-block'}`}
    >
      {triggerWithOnClick}
      {tooltip && (
        <span
          className={`absolute px-2 py-1 rounded bg-surface-hover text-xs text-text-primary transition-opacity duration-100 pointer-events-none whitespace-nowrap z-10 ${
            tooltipPosition === 'below'
              ? 'top-full left-1/2 -translate-x-1/2 mt-1'
              : tooltipPosition === 'above'
                ? 'bottom-full left-1/2 -translate-x-1/2 mb-1'
                : 'left-full top-1/2 -translate-y-1/2 ml-2'
          } ${isOpen ? 'opacity-0' : 'opacity-0 group-hover:opacity-100'}`}
        >
          {tooltip}
        </span>
      )}
      <div
        className={`absolute z-50 bg-background-secondary border border-border rounded-md shadow-lg p-2 transition-all duration-150 ${verticalClass} ${horizontalClass} ${origin} ${
          isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none'
        }`}
      >
        {children}
      </div>
    </div>
  )
}
