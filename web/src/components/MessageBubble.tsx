import { useEffect, useMemo, useState } from 'react'
import {
  isDiffStatLine,
  isFileDiffRow,
  parseBlocks,
  parseDiffStat,
  parseFileDiffRow,
} from '../lib/markdown'
import type { ChatMessage } from '../lib/types'
import { StatusBadge } from './atoms/StatusBadge'
import { MarkdownText } from './molecules/MarkdownText'

type Props = {
  message: ChatMessage
  isLast?: boolean
}

export function MessageBubble({ message, isLast }: Props) {
  const isUser = message.role === 'user'
  const isTool = message.role === 'tool'
  const [expanded, setExpanded] = useState(false)
  const [animate, setAnimate] = useState(false)

  useEffect(() => {
    if (!isUser && !isTool && message.content) {
      setAnimate(true)
    }
  }, [isUser, isTool, message.content])

  const blocks = useMemo(() => {
    if (isUser || isTool) return null
    return parseBlocks(message.content)
  }, [isUser, isTool, message.content])

  if (isTool) {
    return (
      <div className="py-1.5">
        <div
          className="flex items-center gap-2 rounded-lg border border-[#2e2e2e] bg-[#1a1a1a] px-3 py-2 cursor-pointer hover:bg-[#1e1e1e] transition-colors"
          onClick={() => setExpanded(!expanded)}
          onKeyDown={(e) => e.key === 'Enter' && setExpanded(!expanded)}
          role="button"
          tabIndex={0}
        >
          <svg
            className="h-4 w-4 text-[#666]"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2 2 0 0 1-2.83 0a2 2 0 0 1 0-2.83l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
          </svg>
          <span className="text-sm font-medium text-[#ccc] font-mono">{message.toolName}</span>
          {message.toolStatus && <StatusBadge status={message.toolStatus} />}
          <svg
            className={`h-3 w-3 text-[#666] transition-transform ${expanded ? 'rotate-180' : ''}`}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </div>
        {expanded && (
          <div className="mt-2 space-y-2 rounded-lg border border-[#2e2e2e] bg-[#151515] p-3">
            {message.toolArgs && (
              <div>
                <p className="text-[10px] uppercase tracking-[0.2em] text-[#666] mb-1">Action</p>
                <p className="text-xs text-[#aaa] font-mono">{message.toolArgs}</p>
              </div>
            )}
            {message.toolResult && (
              <div>
                <p className="text-[10px] uppercase tracking-[0.2em] text-[#666] mb-1">Result</p>
                <pre className="text-xs text-[#888] font-mono whitespace-pre-wrap overflow-x-auto max-h-[200px]">
                  {message.toolResult}
                </pre>
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  if (isUser) {
    return (
      <div className="flex justify-end py-1">
        <div className="max-w-[70%] space-y-2 rounded-xl bg-[#2e2e2e] px-4 py-2.5 text-sm text-[#e0e0e0] whitespace-pre-wrap">
          {message.content ? <div>{message.content}</div> : null}
          {message.attachments?.length ? (
            <div className="flex flex-wrap gap-2">
              {message.attachments.map((attachment, index) => (
                <span
                  key={`${attachment.path ?? attachment.name ?? 'attachment'}:${index}`}
                  className="rounded-full border border-[#4a4a4a] bg-[#252525] px-3 py-1 text-xs text-[#cfcfcf]"
                >
                  {attachment.name ?? attachment.path ?? 'attachment'}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      </div>
    )
  }

  return (
    <div className={`py-3 ${animate ? 'animate-message-enter' : ''}`}>
      <div className="space-y-3">
        {message.streaming && message.content === '' ? (
          <div className="flex items-center gap-2 text-[#555] text-sm">
            <span className="inline-block h-2 w-2 rounded-full bg-[#555] animate-pulse" />
            <span className="inline-block h-2 w-2 rounded-full bg-[#555] animate-pulse [animation-delay:0.2s]" />
            <span className="inline-block h-2 w-2 rounded-full bg-[#555] animate-pulse [animation-delay:0.4s]" />
          </div>
        ) : blocks && blocks.length > 0 ? (
          blocks.map((block, i) => {
            if (block.type === 'tool') {
              return (
                <div key={`tool-${i}`} className="flex items-center gap-3 text-sm text-[#888]">
                  <span className="rounded bg-[#2a2a2a] px-2 py-0.5 text-[11px] font-medium text-[#aaa] font-mono">
                    {block.label}
                  </span>
                  <span>{block.content}</span>
                </div>
              )
            }

            if (block.type === 'code') {
              return (
                <div
                  key={`code-${i}`}
                  className="rounded-lg border border-[#2e2e2e] bg-[#1a1a1a] overflow-hidden"
                >
                  {block.label && (
                    <div className="border-b border-[#2e2e2e] px-4 py-1.5 text-[10px] text-[#555] font-mono">
                      {block.label}
                    </div>
                  )}
                  <pre className="overflow-x-auto px-4 py-3 text-xs text-[#ccc] font-mono leading-5">
                    <code>{block.content}</code>
                  </pre>
                </div>
              )
            }

            const lines = block.content.split('\n')
            const hasSpecialRows = lines.some((line) => isDiffStatLine(line) || isFileDiffRow(line))

            if (!hasSpecialRows) {
              return <MarkdownText key={`text-${i}`} content={block.content} />
            }

            return (
              <div key={`special-${i}`} className="space-y-2">
                {lines.map((line, j) => {
                  if (isDiffStatLine(line)) {
                    const parsed = parseDiffStat(line)
                    if (!parsed) return null
                    return (
                      <div key={`diffstat-${j}`} className="text-sm text-[#ccc]">
                        <span>{parsed.files} Changed files </span>
                        <span className="text-emerald-400">{parsed.added}</span>
                        <span> </span>
                        <span className="text-red-400">{parsed.removed}</span>
                      </div>
                    )
                  }
                  if (isFileDiffRow(line)) {
                    const parsed = parseFileDiffRow(line)
                    if (!parsed) return null
                    return (
                      <div
                        key={`filediff-${j}`}
                        className="flex items-center justify-between rounded border border-[#2e2e2e] bg-[#1e1e1e] px-3 py-1.5 text-xs"
                      >
                        <span className="text-[#ccc] font-mono">{parsed.filename}</span>
                        <div className="flex items-center gap-2">
                          <span className="text-emerald-400">{parsed.added}</span>
                          <span className="text-red-400">{parsed.removed}</span>
                          <svg
                            width="12"
                            height="12"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="2"
                            className="text-[#555]"
                            aria-hidden="true"
                          >
                            <polyline points="9 18 15 12 9 6" />
                          </svg>
                        </div>
                      </div>
                    )
                  }
                  if (!line.trim()) return <div key={`blank-${j}`} className="h-2" />
                  return <MarkdownText key={`line-${j}`} content={line} />
                })}
              </div>
            )
          })
        ) : (
          <MarkdownText content={message.content} />
        )}

        {message.attachments?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.attachments.map((attachment, index) => (
              <div
                key={`${attachment.path ?? attachment.name ?? 'attachment'}:${index}`}
                className="rounded-lg border border-[#2e2e2e] bg-[#1e1e1e] px-3 py-2 text-xs text-[#bbb]"
              >
                <p className="font-medium text-[#ddd]">
                  {attachment.name ?? attachment.path ?? 'attachment'}
                </p>
                {attachment.caption ? (
                  <p className="mt-1 text-[#888]">{attachment.caption}</p>
                ) : null}
                {attachment.path ? (
                  <p className="mt-1 font-mono text-[#666]">{attachment.path}</p>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}

        {isLast && message.streaming && message.content !== '' && (
          <span className="inline-block h-4 w-0.5 bg-[#ccc] animate-pulse ml-0.5" />
        )}
      </div>
    </div>
  )
}
