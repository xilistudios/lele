export type ContentBlock = {
  type: 'text' | 'tool' | 'code' | 'diff'
  content: string
  label?: string
}

export type InlineToken = {
  type: 'code' | 'bold' | 'italic' | 'link'
  content: string
  href?: string
  index: number
  length: number
}

export function parseBlocks(content: string): ContentBlock[] {
  const lines = content.split('\n')
  const blocks: ContentBlock[] = []
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

export function parseInlineMarkdown(text: string): Array<{ text: string; token?: InlineToken }> {
  const result: Array<{ text: string; token?: InlineToken }> = []
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^\)]+\))/g
  let lastIndex = 0

  for (const match of text.matchAll(pattern)) {
    const token = match[0]
    const index = match.index ?? 0

    if (index > lastIndex) {
      result.push({ text: text.slice(lastIndex, index) })
    }

    if (token.startsWith('`')) {
      result.push({
        text: token.slice(1, -1),
        token: { type: 'code', content: token.slice(1, -1), index, length: token.length },
      })
    } else if (token.startsWith('**')) {
      result.push({
        text: token.slice(2, -2),
        token: { type: 'bold', content: token.slice(2, -2), index, length: token.length },
      })
    } else if (token.startsWith('*')) {
      result.push({
        text: token.slice(1, -1),
        token: { type: 'italic', content: token.slice(1, -1), index, length: token.length },
      })
    } else {
      const linkMatch = token.match(/^\[([^\]]+)\]\(([^\)]+)\)$/)
      if (linkMatch) {
        result.push({
          text: linkMatch[1],
          token: {
            type: 'link',
            content: linkMatch[1],
            href: linkMatch[2],
            index,
            length: token.length,
          },
        })
      }
    }

    lastIndex = index + token.length
  }

  if (lastIndex < text.length) {
    result.push({ text: text.slice(lastIndex) })
  }

  return result.length > 0 ? result : [{ text }]
}

export function parseMarkdownTable(lines: string[]): {
  headers: string[]
  alignments: ('left' | 'center' | 'right')[]
  rows: string[][]
  lineCount: number
} | null {
  if (lines.length < 2) return null

  const isTableLine = (line: string) => /^\s*\|/.test(line.trim())
  const isSeparatorLine = (line: string) => /^\s*\|[\s\-:|]+\|\s*$/.test(line)

  if (!isTableLine(lines[0]) || !isSeparatorLine(lines[1])) {
    return null
  }

  let endIdx = 2
  while (endIdx < lines.length && isTableLine(lines[endIdx])) {
    endIdx++
  }

  const tableLines = lines.slice(0, endIdx)
  const headerCells = tableLines[0].split('|').filter((_, i, arr) => i > 0 && i < arr.length - 1)
  const separatorCells = tableLines[1].split('|').filter((_, i, arr) => i > 0 && i < arr.length - 1)

  const alignments = separatorCells.map((cell) => {
    const trimmed = cell.trim()
    const left = trimmed.startsWith(':')
    const right = trimmed.endsWith(':')
    if (left && right) return 'center' as const
    if (right) return 'right' as const
    return 'left' as const
  })

  const rows: string[][] = []
  for (let i = 2; i < tableLines.length; i++) {
    const cells = tableLines[i].split('|').filter((_, j, arr) => j > 0 && j < arr.length - 1)
    rows.push(cells)
  }

  return {
    headers: headerCells.map((h) => h.trim()),
    alignments,
    rows: rows.map((row) => row.map((c) => c.trim())),
    lineCount: endIdx,
  }
}

export function isDiffStatLine(line: string): boolean {
  return /\d+\s+[Cc]hanged?\s+files?/.test(line)
}

export function isFileDiffRow(line: string): boolean {
  return /^.+\s+\+\d+\s+-\d+/.test(line) && line.trim().split(/\s+/).length <= 4
}

export function parseDiffStat(
  text: string,
): { files: string; added: string; removed: string } | null {
  const match = text.match(/(\d+)\s+[Cc]hanged?\s+files?\s*,?\s*(\+\d+)\s+(-\d+)/)
  if (!match) return null
  return {
    files: match[1],
    added: match[2],
    removed: match[3],
  }
}

export function parseFileDiffRow(
  line: string,
): { filename: string; added: string; removed: string } | null {
  const match = line.match(/^(.+?)\s+(\+\d+)\s+(-\d+)/)
  if (!match) return null
  return {
    filename: match[1].trim(),
    added: match[2],
    removed: match[3],
  }
}

export function hasSpecialDiffRows(content: string): boolean {
  const lines = content.split('\n')
  return lines.some((line) => isDiffStatLine(line) || isFileDiffRow(line))
}
