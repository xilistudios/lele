import { useEffect, useLayoutEffect, useRef, useState } from 'react'

type Position = {
  horizontal: 'left-align' | 'right-align'
  vertical: 'below' | 'above'
}

type Props = {
  isOpen: boolean
  popoverWidth: number
  popoverHeight: number
  padding?: number
}

const DEFAULT_PADDING = 8

function calculatePosition(
  element: HTMLElement,
  popoverWidth: number,
  popoverHeight: number,
  padding: number,
): Position {
  const rect = element.getBoundingClientRect()
  const spaceLeft = rect.left
  const spaceRight = window.innerWidth - rect.right
  const spaceBelow = window.innerHeight - rect.bottom
  const spaceAbove = rect.top

  const horizontal =
    spaceLeft < popoverWidth + padding && spaceRight >= popoverWidth + padding
      ? 'left-align'
      : 'right-align'

  const vertical =
    spaceBelow < popoverHeight + padding && spaceAbove >= popoverHeight + padding
      ? 'above'
      : 'below'

  return { horizontal, vertical }
}

export function usePopoverPosition({
  isOpen,
  popoverWidth,
  popoverHeight,
  padding = DEFAULT_PADDING,
}: Props) {
  const [position, setPosition] = useState<Position>({
    horizontal: 'right-align',
    vertical: 'above',
  })
  const ref = useRef<HTMLDivElement>(null)

  useLayoutEffect(() => {
    if (!isOpen || !ref.current) return
    setPosition(calculatePosition(ref.current, popoverWidth, popoverHeight, padding))
  }, [isOpen, popoverWidth, popoverHeight, padding])

  useEffect(() => {
    if (!isOpen) return

    const onResize = () => {
      if (!ref.current) return
      setPosition(calculatePosition(ref.current, popoverWidth, popoverHeight, padding))
    }

    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [isOpen, popoverWidth, popoverHeight, padding])

  return { position, ref }
}
