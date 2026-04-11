import type { ToolMessageStatus } from '../../lib/types'

type Props = {
  status: ToolMessageStatus
}

export function StatusBadge({ status }: Props) {
  if (status === 'executing') {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-[#2a2a2a] px-1.5 py-0.5 text-[10px] text-[#888]">
        <svg
          className="h-3 w-3 animate-spin"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          aria-hidden="true"
        >
          <path d="M21 12a9 9 0 1 1-6.219-8.56" />
        </svg>
        executing
      </span>
    )
  }
  if (status === 'error') {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-[#3a1a1a] px-1.5 py-0.5 text-[10px] text-[#f08080]">
        <svg
          className="h-3 w-3"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          aria-hidden="true"
        >
          <line x1="18" y1="6" x2="6" y2="18" />
          <line x1="6" y1="6" x2="18" y2="18" />
        </svg>
        error
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded bg-[#1a3a2a] px-1.5 py-0.5 text-[10px] text-[#80f080]">
      <svg
        className="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        aria-hidden="true"
      >
        <polyline points="20 6 9 17 4 12" />
      </svg>
      completed
    </span>
  )
}
