import { type ReactNode, useMemo } from 'react'
import {
  isDiffStatLine,
  isFileDiffRow,
  parseDiffStat,
  parseFileDiffRow,
  parseInlineMarkdown,
  parseMarkdownTable,
} from '../../lib/markdown'

function InlineToken({ text, token }: { text: string; token?: { type: string; href?: string } }) {
  if (!token) return <>{text}</>

  switch (token.type) {
    case 'code':
      return (
        <code className="rounded bg-[#222] px-1 py-0.5 font-mono text-[0.95em] text-[#f0f0f0]">
          {text}
        </code>
      )
    case 'bold':
      return <strong>{text}</strong>
    case 'italic':
      return <em>{text}</em>
    case 'link':
      return (
        <a
          href={token.href ?? '#'}
          target="_blank"
          rel="noreferrer"
          className="text-cyan-300 underline decoration-[#345] underline-offset-2 hover:text-cyan-200"
        >
          {text}
        </a>
      )
    default:
      return <>{text}</>
  }
}

function MarkdownTable({
  headers,
  alignments,
  rows,
}: {
  headers: string[]
  alignments: ('left' | 'center' | 'right')[]
  rows: string[][]
}) {
  const alignClass = (align: string) => {
    if (align === 'center') return 'text-center'
    if (align === 'right') return 'text-right'
    return 'text-left'
  }

  return (
    <div className="overflow-x-auto my-3">
      <table className="min-w-full border-collapse">
        <thead>
          <tr>
            {headers.map((header, i) => (
              <th
                key={`header-${i}`}
                className={`border border-[#2e2e2e] bg-[#252525] px-3 py-2 text-sm font-semibold text-[#f2f2f2] ${alignClass(alignments[i] ?? 'left')}`}
              >
                <InlineMarkdown text={header} />
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => (
            <tr key={`row-${rowIndex}`}>
              {row.map((cell, cellIndex) => (
                <td
                  key={`cell-${rowIndex}-${cellIndex}`}
                  className={`border border-[#2e2e2e] bg-[#1a1a1a] px-3 py-2 text-sm text-[#ccc] ${alignClass(alignments[cellIndex] ?? 'left')}`}
                >
                  <InlineMarkdown text={cell} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DiffStat({ text }: { text: string }) {
  const stat = parseDiffStat(text)
  if (!stat) return <p className="text-sm text-[#ccc]">{text}</p>

  return (
    <div className="text-sm text-[#ccc]">
      <span>{stat.files} Changed files </span>
      <span className="text-emerald-400">{stat.added}</span>
      <span> </span>
      <span className="text-red-400">{stat.removed}</span>
    </div>
  )
}

function FileDiffRow({ line }: { line: string }) {
  const diff = parseFileDiffRow(line)
  if (!diff) return null
  return (
    <div className="flex items-center justify-between rounded border border-[#2e2e2e] bg-[#1e1e1e] px-3 py-1.5 text-xs">
      <span className="text-[#ccc] font-mono">{diff.filename}</span>
      <div className="flex items-center gap-2">
        <span className="text-emerald-400">{diff.added}</span>
        <span className="text-red-400">{diff.removed}</span>
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

function InlineMarkdown({ text }: { text: string }) {
  const tokens = useMemo(() => parseInlineMarkdown(text), [text])

  return (
    <>
      {tokens.map((item, i) => (
        <InlineToken key={i} text={item.text} token={item.token} />
      ))}
    </>
  )
}

export function MarkdownText({ content }: { content: string }) {
  const lines = content.split('\n')
  const chunks: ReactNode[] = useMemo(() => {
    const result: ReactNode[] = []

    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index]
      if (!line.trim()) {
        result.push(<div key={`blank-${index}`} className="h-2" />)
        continue
      }

      const headingMatch = line.match(/^(#{1,3})\s+(.+)$/)
      if (headingMatch) {
        const level = headingMatch[1].length
        const HeadingTag = level === 1 ? 'h3' : level === 2 ? 'h4' : 'h5'
        result.push(
          <HeadingTag key={`heading-${index}`} className="font-semibold text-[#f2f2f2]">
            <InlineMarkdown text={headingMatch[2]} />
          </HeadingTag>,
        )
        continue
      }

      const listMatch = line.match(/^[-*]\s+(.+)$/)
      if (listMatch) {
        result.push(
          <div key={`list-${index}`} className="flex gap-2 pl-4 text-sm leading-6 text-[#ccc]">
            <span className="mt-2 h-1.5 w-1.5 rounded-full bg-[#666]" />
            <InlineMarkdown text={listMatch[1]} />
          </div>,
        )
        continue
      }

      const table = parseMarkdownTable(lines.slice(index))
      if (table) {
        result.push(
          <MarkdownTable
            key={`table-${index}`}
            headers={table.headers}
            alignments={table.alignments}
            rows={table.rows}
          />,
        )
        index += table.lineCount - 1
        continue
      }

      if (isDiffStatLine(line)) {
        result.push(<DiffStat key={`diff-${index}`} text={line} />)
        continue
      }

      if (isFileDiffRow(line)) {
        result.push(<FileDiffRow key={`file-${index}`} line={line} />)
        continue
      }

      result.push(
        <p key={`p-${index}`} className="text-sm leading-6 text-[#ccc] whitespace-pre-wrap">
          <InlineMarkdown text={line} />
        </p>,
      )
    }

    return result
  }, [lines])

  return <div className="space-y-2">{chunks}</div>
}
