import {
  type ReactNode,
  cloneElement,
  isValidElement,
  useCallback,
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
  /**
   * Controlled open state. Leave it undefined to keep the popover
   * self-managed (the default): the existing callers pass none of these props
   * and behave exactly as before.
   */
  open?: boolean
  /** Notified whenever the popover wants to change its open state. */
  onOpenChange?: (open: boolean) => void
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
  open,
  onOpenChange,
}: Props) {
  // `open === undefined` keeps the popover uncontrolled, so callers that do not
  // care about the open state keep the original behaviour untouched.
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false)
  const isControlled = open !== undefined
  const isOpen = isControlled ? open : uncontrolledOpen
  const setOpen = useCallback(
    (next: boolean) => {
      if (!isControlled) setUncontrolledOpen(next)
      onOpenChange?.(next)
    },
    [isControlled, onOpenChange],
  )
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
        setOpen(false)
      }
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
      }
    }

    document.addEventListener('mousedown', onClickOutside)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onClickOutside)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [isOpen, ref, setOpen])

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
          onClick: () => setOpen(!isOpen),
          onKeyDown: (e: React.KeyboardEvent) => {
            if (e.key === 'Enter' || e.key === ' ') {
              setOpen(!isOpen)
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
        className={`bg-background-secondary border border-border absolute z-50 rounded-md shadow-pop p-2 transition-all duration-fast ${verticalClass} ${horizontalClass} ${origin} ${
          isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none'
        }`}
      >
        {children}
      </div>
    </div>
  )
}
