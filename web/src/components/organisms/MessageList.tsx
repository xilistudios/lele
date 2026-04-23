import { useCallback, useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { MessageBubble } from '../MessageBubble'

export function MessageList() {
  const navigate = useNavigate()
  const { messages, approvalRequest, onApprove, currentSessionKey } = useAppLogicContext()
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const lastMessageId = messages[messages.length - 1]?.id

  const handleNavigateToSession = useCallback(
    (sessionKey: string) => {
      if (!currentSessionKey) return

      navigate(
        `/chat/${encodeURIComponent(currentSessionKey)}/subagent/${encodeURIComponent(sessionKey)}`,
      )
    },
    [currentSessionKey, navigate],
  )

  useEffect(() => {
    if (!lastMessageId && messages.length === 0) return
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [lastMessageId, messages.length])

  if (messages.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-sm text-text-tertiary">Start a conversation</p>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-3xl space-y-1">
      {messages.map((message, index) => (
        <MessageBubble
          key={message.id}
          message={message}
          isLast={index === messages.length - 1}
          onNavigateToSession={handleNavigateToSession}
        />
      ))}
      {approvalRequest && (
        <div className="py-2">
          <div className="rounded-lg border border-border bg-background-primary p-4">
            <p className="text-sm font-medium text-text-secondary mb-2">
              {approvalRequest.command}
            </p>
            <p className="text-xs text-text-secondary mb-4">{approvalRequest.reason}</p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => onApprove(true)}
                className="rounded-md bg-state-success-light px-3 py-1.5 text-xs text-state-success hover:bg-state-success-light/80"
              >
                Approve
              </button>
              <button
                type="button"
                onClick={() => onApprove(false)}
                className="rounded-md bg-state-error-light px-3 py-1.5 text-xs text-state-error hover:bg-state-error-light/80"
              >
                Reject
              </button>
            </div>
          </div>
        </div>
      )}
      <div ref={messagesEndRef} />
    </div>
  )
}
