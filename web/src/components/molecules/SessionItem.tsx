import { useTranslation } from 'react-i18next'

type Props = {
  sessionKey: string
  sessionName?: string
  messageCount: number
  selected?: boolean
  onSelect: () => void
  onDelete: () => void
  collapsed?: boolean
}

const formatSessionTitle = (sessionKey: string, sessionName?: string) => {
  if (sessionName?.trim()) {
    return sessionName
  }
  const parts = sessionKey.split(':')
  return parts.length > 2 ? `Session ${parts[parts.length - 1]}` : sessionKey
}

export function SessionItem({
  sessionKey,
  sessionName,
  messageCount,
  selected = false,
  onSelect,
  onDelete,
  collapsed = false,
}: Props) {
  const { t } = useTranslation()

  if (collapsed) {
    return (
      <button
        onClick={onSelect}
        type="button"
        title={formatSessionTitle(sessionKey, sessionName)}
        className={`flex w-full items-center justify-center rounded-md p-2 transition-colors ${
          selected ? 'bg-[#2e2e2e] text-white' : 'text-[#999] hover:bg-[#272727] hover:text-[#ccc]'
        }`}
      >
        <span className="text-xs text-[#555]">#</span>
      </button>
    )
  }

  return (
    <div
      onClick={onSelect}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          onSelect()
        }
      }}
      role="button"
      tabIndex={0}
      className={`group flex w-full cursor-pointer items-start gap-2 rounded-md px-3 py-2 text-left transition-colors ${
        selected ? 'bg-[#2e2e2e] text-white' : 'text-[#999] hover:bg-[#272727] hover:text-[#ccc]'
      }`}
    >
      <span className="mt-0.5 text-xs text-[#555]">#</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs leading-5">
          {formatSessionTitle(sessionKey, sessionName)}
        </span>
        <span className="block text-[10px] text-[#666]">
          {messageCount === 1
            ? t('chat.messageCount_one', { count: messageCount })
            : t('chat.messageCount_other', { count: messageCount })}
        </span>
      </span>
      <button
        onClick={(event) => {
          event.stopPropagation()
          onDelete()
        }}
        type="button"
        aria-label={t('chat.deleteSession')}
        className="ml-auto text-[#666] opacity-0 transition-opacity hover:text-[#f0b4b4] group-hover:opacity-100"
      >
        <svg
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          aria-hidden="true"
        >
          <path d="M3 6h18" />
          <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
        </svg>
      </button>
    </div>
  )
}
