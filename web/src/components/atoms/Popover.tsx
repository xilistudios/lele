import { cloneElement, isValidElement, type ReactNode, useEffect, useState } from 'react'
import { usePopoverPosition } from '../../hooks/usePopoverPosition'

type Props = {
  trigger: ReactNode
  children: ReactNode
  popoverWidth?: number
  popoverHeight?: number
  block?: boolean
}

const DEFAULT_POPOVER_WIDTH = 180
const DEFAULT_POPOVER_HEIGHT = 100

const ORIGIN_MAP = {
  below: { 'right-align': 'origin-top-right', 'left-align': 'origin-top-left' },
  above: { 'right-align': 'origin-bottom-right', 'left-align': 'origin-bottom-left' },
} as const

export function Popover({
  trigger,
  children,
  popoverWidth = DEFAULT_POPOVER_WIDTH,
  popoverHeight = DEFAULT_POPOVER_HEIGHT,
  block = false,
}: Props) {
  const [isOpen, setIsOpen] = useState(false)
  const { position, ref } = usePopoverPosition({
    isOpen,
    popoverWidth,
    popoverHeight,
  })

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
    ? cloneElement(trigger as React.ReactElement<{ onClick?: () => void; onKeyDown?: (e: React.KeyboardEvent) => void }>, {
        onClick: () => setIsOpen((prev) => !prev),
        onKeyDown: (e: React.KeyboardEvent) => {
          if (e.key === 'Enter' || e.key === ' ') {
            setIsOpen((prev) => !prev)
          }
        },
      })
    : trigger

  return (
    <div ref={ref} className={`group relative ${block ? 'block w-full' : 'inline-block'}`}>
      {triggerWithOnClick}
      <div
        className={`absolute z-50 bg-background-tertiary border border-border rounded-md shadow-lg p-2 transition-all duration-150 ${verticalClass} ${horizontalClass} ${origin} ${
          isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none'
        }`}
      >
        {children}
      </div>
    </div>
  )
}
