import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

type Props = {
  status: 'disconnected' | 'connecting' | 'connected'
  apiUrl?: string
}

type Position = {
  horizontal: 'left-align' | 'right-align'
  vertical: 'below' | 'above'
}

export function ConnectionIndicator({ status, apiUrl }: Props) {
  const { t } = useTranslation()
  const [isOpen, setIsOpen] = useState(false)
  const [position, setPosition] = useState<Position>({ horizontal: 'right-align', vertical: 'below' })
  const containerRef = useRef<HTMLDivElement>(null)

  const dotColor =
    status === 'connected'
      ? 'bg-emerald-400'
      : status === 'connecting'
        ? 'bg-yellow-400 animate-pulse-dot'
        : 'bg-red-400'

  const statusColor =
    status === 'connected'
      ? 'text-emerald-400'
      : status === 'connecting'
        ? 'text-yellow-400'
        : 'text-gray-500'

  const displayUrl = apiUrl ? apiUrl.replace(/^https?:\/\//, '') : 'N/A'
  const statusText = status.charAt(0).toUpperCase() + status.slice(1)

  const calculatePosition = useCallback(() => {
    if (!containerRef.current) return

    const triggerRect = containerRef.current.getBoundingClientRect()
    const popoverWidth = 180
    const popoverHeight = 80
    const padding = 8

    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight

    // Horizontal: right-align (popover expands left) is default
    // Use left-align (popover expands right) only if not enough space on left
    let horizontal: 'left-align' | 'right-align' = 'right-align'
    
    const spaceOnLeft = triggerRect.left
    const spaceOnRight = viewportWidth - triggerRect.right
    
    if (spaceOnLeft < popoverWidth + padding && spaceOnRight >= popoverWidth + padding) {
      horizontal = 'left-align'
    }

    // Vertical: below is default, above if not enough space below
    let vertical: 'below' | 'above' = 'below'
    
    const spaceBelow = viewportHeight - triggerRect.bottom
    const spaceAbove = triggerRect.top
    
    if (spaceBelow < popoverHeight + padding && spaceAbove >= popoverHeight + padding) {
      vertical = 'above'
    }

    setPosition({ horizontal, vertical })
  }, [])

  useLayoutEffect(() => {
    if (isOpen) {
      calculatePosition()
    }
  }, [isOpen, calculatePosition])

  useEffect(() => {
    if (!isOpen) return

    const handleResize = () => calculatePosition()
    window.addEventListener('resize', handleResize)

    return () => window.removeEventListener('resize', handleResize)
  }, [isOpen, calculatePosition])

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false)
      }
    }

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside)
    }

    return () => {
      document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [isOpen])

  const popoverClasses = [
    'absolute z-50 w-[180px] rounded-lg border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 shadow-lg transition-all duration-150',
    position.vertical === 'below' ? 'top-full mt-1' : 'bottom-full mb-1',
    position.horizontal === 'right-align' ? 'right-0' : 'left-0',
    position.vertical === 'below' && position.horizontal === 'right-align' ? 'origin-top-right' : '',
    position.vertical === 'below' && position.horizontal === 'left-align' ? 'origin-top-left' : '',
    position.vertical === 'above' && position.horizontal === 'right-align' ? 'origin-bottom-right' : '',
    position.vertical === 'above' && position.horizontal === 'left-align' ? 'origin-bottom-left' : '',
    isOpen ? 'opacity-100 scale-100' : 'opacity-0 scale-95 pointer-events-none',
  ].join(' ')

  return (
    <div ref={containerRef} className={`relative ${!isOpen ? 'group' : ''}`}>
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="rounded p-1.5 text-gray-500 hover:bg-gray-700/50 hover:text-gray-400 transition-colors duration-150"
        aria-label={`Connection status: ${status}. Click for details.`}
      >
        <div className="relative">
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
            <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
            <line x1="6" y1="6" x2="6.01" y2="6" />
            <line x1="6" y1="18" x2="6.01" y2="18" />
          </svg>
          <span
            className={`absolute -top-1 -right-1 block h-2.5 w-2.5 rounded-full border border-[#1a1a1a] ${dotColor}`}
            aria-hidden="true"
          />
        </div>
      </button>

      {/* Tooltip - hidden when popover is open */}
      <span 
        className={`absolute top-full left-1/2 -translate-x-1/2 mt-1 px-2 py-1 rounded bg-[#2a2a2a] text-xs text-[#ccc] transition-opacity duration-100 pointer-events-none whitespace-nowrap ${
          isOpen ? 'opacity-0' : 'opacity-0 group-hover:opacity-100'
        }`}
      >
        {t('common.status')}
      </span>

      {/* Popover */}
      <div
        className={popoverClasses}
        role="menu"
        aria-label="Connection details"
      >
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${dotColor}`} />
          <span className={`text-xs font-medium ${statusColor}`}>{statusText}</span>
        </div>
        <div className="mt-2 rounded bg-[#2a2a2a] px-2 py-1.5 text-xs font-mono text-[#ccc]">
          {displayUrl}
        </div>
      </div>
    </div>
  )
}