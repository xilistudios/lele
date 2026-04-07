import { useEffect, useRef } from 'react'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { MessageBubble } from '../MessageBubble'

export function MessageList() {
  const { messages, approvalRequest, onApprove } = useAppLogicContext()
  const messagesEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages])

  if (messages.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-sm text-[#444]">Start a conversation</p>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-3xl space-y-1">
      {messages.map((message, index) => (
        <MessageBubble key={message.id} message={message} isLast={index === messages.length - 1} />
      ))}
      {approvalRequest && (
        <div className="py-2">
          <div className="rounded-lg border border-[#2e2e2e] bg-[#1a1a1a] p-4">
            <p className="text-sm font-medium text-[#ccc] mb-2">{approvalRequest.command}</p>
            <p className="text-xs text-[#888] mb-4">{approvalRequest.reason}</p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => onApprove(true)}
                className="rounded-md bg-[#1a3a2a] px-3 py-1.5 text-xs text-[#80f080] hover:bg-[#2a4a3a]"
              >
                Approve
              </button>
              <button
                type="button"
                onClick={() => onApprove(false)}
                className="rounded-md bg-[#3a1a1a] px-3 py-1.5 text-xs text-[#f08080] hover:bg-[#4a2a2a]"
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
