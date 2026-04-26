import { useEffect, useMemo, useState } from 'react'
import {
  isDiffStatLine,
  isFileDiffRow,
  parseBlocks,
  parseDiffStat,
  parseFileDiffRow,
} from '../lib/markdown'
import type { Attachment, ChatMessage } from '../lib/types'
import { MarkdownText } from './molecules/MarkdownText'
import { ToolCallDisplay } from './molecules/ToolCallDisplay'

const IMAGE_EXTENSIONS = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.svg'])

function isImageByExtension(name: string): boolean {
  const ext = name.toLowerCase().split('.').pop()
  return ext ? IMAGE_EXTENSIONS.has(`.${ext}`) : false
}

function isImageAttachment(attachment: Attachment): boolean {
  // Check mime_type first (most reliable)
  if (attachment.mime_type?.startsWith('image/')) return true
  // Fall back to extension check on name or path
  if (attachment.name && isImageByExtension(attachment.name)) return true
  if (attachment.path && isImageByExtension(attachment.path)) return true
  return false
}

function buildFileUrl(apiUrl: string, path: string): string {
  const base = apiUrl.replace(/\/$/, '')
  return `${base}/api/v1/files/view?path=${encodeURIComponent(path)}`
}

type Props = {
  message: ChatMessage
  isLast?: boolean
  onNavigateToSession?: (sessionKey: string) => void
  apiUrl?: string
}

export function MessageBubble({ message, isLast, onNavigateToSession, apiUrl }: Props) {
  const isUser = message.role === 'user'
  const isTool = message.role === 'tool'
  const [expanded, setExpanded] = useState(false)
  const [animate, setAnimate] = useState(false)
  const [thinkingOpen, setThinkingOpen] = useState(message.streaming && !!message.reasoningContent)

  // Auto-open thinking when streaming starts
  useEffect(() => {
    if (message.streaming && message.reasoningContent) {
      setThinkingOpen(true)
    }
  }, [message.streaming, message.reasoningContent])

  useEffect(() => {
    if (!isUser && !isTool && message.content) {
      setAnimate(true)
    }
  }, [isUser, isTool, message.content])

  const blocks = useMemo(() => {
    if (isUser || isTool) return null
    return parseBlocks(message.content)
  }, [isUser, isTool, message.content])

  const hasThinking = !!message.reasoningContent

  if (isTool) {
    const subagentSessionKey = message.subagentSessionKey

    return (
      <div className="py-1.5">
        <ToolCallDisplay
          toolName={message.toolName}
          toolArgs={message.toolArgs}
          toolResult={message.toolResult}
          toolStatus={message.toolStatus}
          subagentSessionKey={subagentSessionKey}
          onNavigateToSession={onNavigateToSession}
          expanded={expanded}
          onToggleExpand={() => setExpanded(!expanded)}
        />
      </div>
    )
  }

  if (isUser) {
    const imageAttachments = message.attachments?.filter(isImageAttachment) ?? []
    const nonImageAttachments = message.attachments?.filter((a) => !isImageAttachment(a)) ?? []

    return (
      <div className="flex justify-end py-1">
        <div className="max-w-[70%] space-y-2 rounded-xl bg-surface-muted px-4 py-2.5 text-sm text-text-primary whitespace-pre-wrap">
          {message.content ? <div>{message.content}</div> : null}
          {imageAttachments.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {imageAttachments.map((attachment, index) => (
                <img
                  key={`${attachment.path ?? attachment.name ?? 'img'}:${index}`}
                  src={buildFileUrl(apiUrl ?? '', attachment.path ?? '')}
                  alt={attachment.name ?? 'image'}
                  className="max-w-full rounded-lg object-contain max-h-96"
                  loading="lazy"
                />
              ))}
            </div>
          ) : null}
          {nonImageAttachments.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {nonImageAttachments.map((attachment, index) => (
                <span
                  key={`${attachment.path ?? attachment.name ?? 'attachment'}:${index}`}
                  className="rounded-full border border-border bg-background-secondary px-3 py-1 text-xs text-text-primary"
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
        {hasThinking ? (
          <div className="rounded-lg border border-border bg-background-secondary/50 overflow-hidden">
            <button
              type="button"
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-background-secondary transition-colors"
              onClick={() => setThinkingOpen(!thinkingOpen)}
              aria-expanded={thinkingOpen}
            >
              <svg
                className={`h-3.5 w-3.5 text-text-tertiary transition-transform ${thinkingOpen ? 'rotate-90' : ''}`}
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <polyline points="9 18 15 12 9 6" />
              </svg>
              <span className="text-xs text-text-tertiary italic">
                {message.streaming && message.reasoningContent ? 'Thinking…' : 'Thinking'}
              </span>
              {message.streaming && message.reasoningContent && (
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-text-tertiary animate-pulse ml-1" />
              )}
            </button>
            {thinkingOpen && (
              <div className="px-3 pb-2">
                <p className="text-xs text-text-tertiary italic whitespace-pre-wrap">
                  {message.reasoningContent}
                </p>
              </div>
            )}
          </div>
        ) : null}
        {message.streaming && message.content === '' && !hasThinking ? (
          <div className="flex items-center gap-2 text-text-tertiary text-sm">
            <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse" />
            <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse [animation-delay:0.2s]" />
            <span className="inline-block h-2 w-2 rounded-full bg-text-tertiary animate-pulse [animation-delay:0.4s]" />
          </div>
        ) : blocks && blocks.length > 0 ? (
          blocks.map((block, i) => {
            if (block.type === 'tool') {
              return (
                <div
                  key={`toolblock-${block.label ?? 'tool'}-${i}`}
                  className="flex items-center gap-3 text-sm text-text-secondary"
                >
                  <span className="rounded-md px-2 py-1 bg-surface-hover text-xs font-medium font-mono text-text-secondary">
                    {block.label}
                  </span>
                  <span>{block.content}</span>
                </div>
              )
            }

            if (block.type === 'code') {
              return (
                <div
                  key={`codeblock-${block.label ?? 'code'}-${i}`}
                  className="rounded-lg border border-border bg-background-primary overflow-hidden"
                >
                  {block.label && (
                    <div className="border-b border-border px-4 py-1.5 text-[10px] text-text-tertiary font-mono">
                      {block.label}
                    </div>
                  )}
                  <pre className="overflow-x-auto px-4 py-3 text-xs text-text-secondary font-mono leading-5">
                    <code>{block.content}</code>
                  </pre>
                </div>
              )
            }

            const lines = block.content.split('\n')
            const hasSpecialRows = lines.some((line) => isDiffStatLine(line) || isFileDiffRow(line))

            if (!hasSpecialRows) {
              return (
                <MarkdownText
                  key={`textblock-${block.content.slice(0, 50)}-${i}`}
                  content={block.content}
                />
              )
            }

            return (
              <div key={`specialblock-${block.content.slice(0, 50)}-${i}`} className="space-y-2">
                {lines.map((line, j) => {
                  if (isDiffStatLine(line)) {
                    const parsed = parseDiffStat(line)
                    if (!parsed) return null
                    return (
                      <div
                        key={`diffstat-${line.slice(0, 40)}-${j}`}
                        className="text-sm text-text-secondary"
                      >
                        <span>{parsed.files} Changed files </span>
                        <span className="text-diff-addition">{parsed.added}</span>
                        <span> </span>
                        <span className="text-diff-deletion">{parsed.removed}</span>
                      </div>
                    )
                  }
                  if (isFileDiffRow(line)) {
                    const parsed = parseFileDiffRow(line)
                    if (!parsed) return null
                    return (
                      <div
                        key={`filediff-${parsed.filename}-${j}`}
                        className="flex items-center justify-between rounded-lg border border-border bg-background-secondary px-3 py-1.5 text-xs"
                      >
                        <span className="text-text-primary font-mono">{parsed.filename}</span>
                        <div className="flex items-center gap-2">
                          <span className="text-diff-addition">{parsed.added}</span>
                          <span className="text-diff-deletion">{parsed.removed}</span>
                          <svg
                            width="12"
                            height="12"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="2"
                            className="text-text-tertiary"
                            aria-hidden="true"
                          >
                            <polyline points="9 18 15 12 9 6" />
                          </svg>
                        </div>
                      </div>
                    )
                  }
                  if (!line.trim())
                    // biome-ignore lint/suspicious/noArrayIndexKey: blank lines have no content for stable keys
                    return <div key={`blankline-${j}`} className="h-2" />
                  return <MarkdownText key={`line-${line.slice(0, 40)}-${j}`} content={line} />
                })}
              </div>
            )
          })
        ) : (
          <MarkdownText content={message.content} />
        )}

        {message.attachments?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.attachments.map((attachment, index) => {
              if (isImageAttachment(attachment)) {
                return (
                  <img
                    key={`${attachment.path ?? attachment.name ?? 'img'}:${index}`}
                    src={buildFileUrl(apiUrl ?? '', attachment.path ?? '')}
                    alt={attachment.name ?? 'image'}
                    className="max-w-full rounded-lg object-contain max-h-96"
                    loading="lazy"
                  />
                )
              }
              return (
                <div
                  key={`${attachment.path ?? attachment.name ?? 'attachment'}:${index}`}
                  className="rounded-lg border border-border bg-background-secondary px-3 py-2 text-xs text-text-secondary"
                >
                  <p className="font-medium text-text-primary">
                    {attachment.name ?? attachment.path ?? 'attachment'}
                  </p>
                  {attachment.caption ? (
                    <p className="mt-1 text-text-secondary">{attachment.caption}</p>
                  ) : null}
                  {attachment.path ? (
                    <p className="mt-1 font-mono text-text-tertiary">{attachment.path}</p>
                  ) : null}
                </div>
              )
            })}
          </div>
        ) : null}

        {isLast && message.streaming && message.content !== '' && (
          <span className="inline-block h-4 w-0.5 bg-text-secondary animate-pulse ml-0.5" />
        )}
      </div>
    </div>
  )
}
