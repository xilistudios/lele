import { type ReactNode, useMemo, useState } from 'react'
import type { ChatMessage, ToolMessageStatus } from '../lib/types'

type Props = {
  message: ChatMessage
  isLast?: boolean
}

const StatusBadge = ({ status }: { status: ToolMessageStatus }) => {
  if (status === 'executing') {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-[#2a2a2a] px-1.5 py-0.5 text-[10px] text-[#888]">
        <svg
          className="h-3 w-3 animate-spin"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
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
      >
        <polyline points="20 6 9 17 4 12" />
      </svg>
      completed
    </span>
  )
}

// Detect shell tool calls embedded in content via a simple heuristic:
// Lines that start with "Shell " or "Build " followed by a description
function parseBlocks(content: string) {
  const lines = content.split('\n')
  const blocks: Array<{
    type: 'text' | 'tool' | 'code' | 'diff'
    content: string
    label?: string
  }> = []
  let codeBuffer: string[] = []
  let inCode = false
  let codeLang = ''
  let textBuffer: string[] = []

  const flushText = () => {
    if (textBuffer.length > 0) {
      const joined = textBuffer.join('\n').trim()
      if (joined) blocks.push({ type: 'text', content: joined })
      textBuffer = []
    }
  }

  for (const line of lines) {
    // Code fences
    if (line.startsWith('```')) {
      if (inCode) {
        blocks.push({ type: 'code', content: codeBuffer.join('\n'), label: codeLang })
        codeBuffer = []
        inCode = false
        codeLang = ''
      } else {
        flushText()
        inCode = true
        codeLang = line.slice(3).trim()
      }
      continue
    }

    if (inCode) {
      codeBuffer.push(line)
      continue
    }

    // Tool lines: "Shell  Description" or "Build  Description"
    const toolMatch = line.match(/^(Shell|Build|Bash|Tool)\s{1,2}(.+)$/)
    if (toolMatch) {
      flushText()
      blocks.push({ type: 'tool', content: toolMatch[2], label: toolMatch[1] })
      continue
    }

    textBuffer.push(line)
  }

  if (inCode && codeBuffer.length > 0) {
    blocks.push({ type: 'code', content: codeBuffer.join('\n'), label: codeLang })
  }
  flushText()

  return blocks
}

// Simple diff stat renderer: "2 Changed files +47 -2"
function DiffStat({ text }: { text: string }) {
  const match = text.match(/(\d+)\s+[Cc]hanged?\s+files?\s+(\+\d+)\s+(-\d+)/)
  if (!match) return <p className="text-sm text-[#ccc]">{text}</p>

  return (
    <div className="text-sm text-[#ccc]">
      <span>{match[1]} Changed files </span>
      <span className="text-emerald-400">{match[2]}</span>
      <span> </span>
      <span className="text-red-400">{match[3]}</span>
    </div>
  )
}

// File diff row: "go.mod  +17 -2 >"
function FileDiffRow({ line }: { line: string }) {
  const match = line.match(/^(.+?)\s+(\+\d+)\s+(-\d+)/)
  if (!match) return null
  return (
    <div className="flex items-center justify-between rounded border border-[#2e2e2e] bg-[#1e1e1e] px-3 py-1.5 text-xs">
      <span className="text-[#ccc] font-mono">{match[1].trim()}</span>
      <div className="flex items-center gap-2">
        <span className="text-emerald-400">{match[2]}</span>
        <span className="text-red-400">{match[3]}</span>
        <svg
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className="text-[#555]"
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </div>
    </div>
  )
}

function renderInlineMarkdown(text: string) {
  const parts: ReactNode[] = []
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^\)]+\))/g
  let lastIndex = 0

  for (const match of text.matchAll(pattern)) {
    const token = match[0]
    const index = match.index ?? 0
    if (index > lastIndex) {
      parts.push(text.slice(lastIndex, index))
    }

    if (token.startsWith('`')) {
      parts.push(
        <code
          key={`${index}-code`}
          className="rounded bg-[#222] px-1 py-0.5 font-mono text-[0.95em] text-[#f0f0f0]"
        >
          {token.slice(1, -1)}
        </code>,
      )
    } else if (token.startsWith('**')) {
      parts.push(<strong key={`${index}-bold`}>{token.slice(2, -2)}</strong>)
    } else if (token.startsWith('*')) {
      parts.push(<em key={`${index}-italic`}>{token.slice(1, -1)}</em>)
    } else {
      const linkMatch = token.match(/^\[([^\]]+)\]\(([^\)]+)\)$/)
      if (linkMatch) {
        parts.push(
          <a
            key={`${index}-link`}
            href={linkMatch[2]}
            target="_blank"
            rel="noreferrer"
            className="text-cyan-300 underline decoration-[#345] underline-offset-2 hover:text-cyan-200"
          >
            {linkMatch[1]}
          </a>,
        )
      }
    }

    lastIndex = index + token.length
  }

  if (lastIndex < text.length) {
    parts.push(text.slice(lastIndex))
  }

  return parts.length > 0 ? parts : [text]
}

function MarkdownText({ content }: { content: string }) {
  const lines = content.split('\n')
  const chunks: ReactNode[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    if (!line.trim()) {
      chunks.push(<div key={`blank-${index}`} className="h-2" />)
      continue
    }

    const headingMatch = line.match(/^(#{1,3})\s+(.+)$/)
    if (headingMatch) {
      const level = headingMatch[1].length
      const HeadingTag = level === 1 ? 'h3' : level === 2 ? 'h4' : 'h5'
      chunks.push(
        <HeadingTag key={`heading-${index}`} className="font-semibold text-[#f2f2f2]">
          {renderInlineMarkdown(headingMatch[2])}
        </HeadingTag>,
      )
      continue
    }

    const listMatch = line.match(/^[-*]\s+(.+)$/)
    if (listMatch) {
      chunks.push(
        <div key={`list-${index}`} className="flex gap-2 pl-4 text-sm leading-6 text-[#ccc]">
          <span className="mt-2 h-1.5 w-1.5 rounded-full bg-[#666]" />
          <span>{renderInlineMarkdown(listMatch[1])}</span>
        </div>,
      )
      continue
    }

    chunks.push(
      <p key={`p-${index}`} className="text-sm leading-6 text-[#ccc] whitespace-pre-wrap">
        {renderInlineMarkdown(line)}
      </p>,
    )
  }

  return <div className="space-y-2">{chunks}</div>
}

export function MessageBubble({ message, isLast }: Props) {
  const isUser = message.role === 'user'
  const isTool = message.role === 'tool'
  const [expanded, setExpanded] = useState(false)

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
        >
          <svg
            className="h-4 w-4 text-[#666]"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
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

  // Assistant message — plain layout like OpenCode
  return (
    <div className="py-3">
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
                <div key={i} className="flex items-center gap-3 text-sm text-[#888]">
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
                  key={i}
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

            // Plain text — check for diff stats and file rows
            const lines = block.content.split('\n')
            const hasSpecialRows = lines.some(
              (line) =>
                /\d+\s+[Cc]hanged?\s+files?/.test(line) ||
                (/^.+\s+\+\d+\s+-\d+/.test(line) && line.trim().split(/\s+/).length <= 4),
            )

            if (!hasSpecialRows) {
              return <MarkdownText key={i} content={block.content} />
            }

            return (
              <div key={i} className="space-y-2">
                {lines.map((line, j) => {
                  if (/\d+\s+[Cc]hanged?\s+files?/.test(line)) {
                    return <DiffStat key={j} text={line} />
                  }
                  if (/^.+\s+\+\d+\s+-\d+/.test(line) && line.trim().split(/\s+/).length <= 4) {
                    return <FileDiffRow key={j} line={line} />
                  }
                  if (!line.trim()) return <div key={j} className="h-2" />
                  return <MarkdownText key={j} content={line} />
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
