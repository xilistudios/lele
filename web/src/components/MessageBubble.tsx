import { useMemo } from 'react'
import type { ChatMessage } from '../lib/types'

type Props = {
  message: ChatMessage
  isLast?: boolean
}

// Detect shell tool calls embedded in content via a simple heuristic:
// Lines that start with "Shell " or "Build " followed by a description
function parseBlocks(content: string) {
  const lines = content.split('\n')
  const blocks: Array<{ type: 'text' | 'tool' | 'code' | 'diff'; content: string; label?: string }> = []
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
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-[#555]">
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </div>
    </div>
  )
}

export function MessageBubble({ message, isLast }: Props) {
  const isUser = message.role === 'user'

  const blocks = useMemo(() => {
    if (isUser) return null
    return parseBlocks(message.content)
  }, [isUser, message.content])

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
                <div key={i} className="rounded-lg border border-[#2e2e2e] bg-[#1a1a1a] overflow-hidden">
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
                  return (
                    <p key={j} className="text-sm text-[#ccc] leading-6 whitespace-pre-wrap">
                      {line}
                    </p>
                  )
                })}
              </div>
            )
          })
        ) : (
          <div className="space-y-2">
            {message.content.split('\n').map((line, i) => {
              if (!line.trim()) return <div key={i} className="h-2" />
              return (
                <p key={i} className="text-sm text-[#ccc] leading-6 whitespace-pre-wrap">
                  {line}
                </p>
              )
            })}
          </div>
        )}

        {message.attachments?.length ? (
          <div className="flex flex-wrap gap-2">
            {message.attachments.map((attachment, index) => (
              <div
                key={`${attachment.path ?? attachment.name ?? 'attachment'}:${index}`}
                className="rounded-lg border border-[#2e2e2e] bg-[#1e1e1e] px-3 py-2 text-xs text-[#bbb]"
              >
                <p className="font-medium text-[#ddd]">{attachment.name ?? attachment.path ?? 'attachment'}</p>
                {attachment.caption ? <p className="mt-1 text-[#888]">{attachment.caption}</p> : null}
                {attachment.path ? <p className="mt-1 font-mono text-[#666]">{attachment.path}</p> : null}
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
