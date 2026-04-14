type Props = {
  status: 'disconnected' | 'connecting' | 'connected'
  showLabel?: boolean
}

export function ConnectionIndicator({ status, showLabel = false }: Props) {
  const className =
    status === 'connected'
      ? 'text-emerald-400'
      : status === 'connecting'
        ? 'text-yellow-400'
        : 'text-gray-500'

  return (
    <div className={`flex items-center gap-1.5 ${className}`}>
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        aria-hidden="true"
      >
        <rect x="2" y="2" width="20" height="8" rx="2" ry="2" />
        <rect x="2" y="14" width="20" height="8" rx="2" ry="2" />
        <line x1="6" y1="6" x2="6.01" y2="6" />
        <line x1="6" y1="18" x2="6.01" y2="18" />
      </svg>
      {showLabel && <span className="text-[10px] whitespace-nowrap">{status}</span>}
    </div>
  )
}
