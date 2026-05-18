import type { ToolMessageStatus } from '../../lib/types'
import { Badge } from './Badge'

type Props = {
  status: ToolMessageStatus
}

export function StatusBadge({ status }: Props) {
  if (status === 'executing') {
    return (
      <Badge variant="info">
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
        Ejecutando
      </Badge>
    )
  }
  if (status === 'error') {
    return (
      <Badge variant="error">
        <svg
          className="h-3 w-3"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" />
          <line x1="15" y1="9" x2="9" y2="15" />
          <line x1="9" y1="9" x2="15" y2="15" />
        </svg>
        Error
      </Badge>
    )
  }
  // Don't show badge for completed/success state - it's visual noise
  return null
}
