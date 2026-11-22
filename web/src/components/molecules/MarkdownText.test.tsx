import { afterEach, beforeAll, beforeEach, describe, expect, mock, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import * as markdown from '../../lib/markdown'

const realParseMarkdownTable = markdown.parseMarkdownTable
const realParseDiffStat = markdown.parseDiffStat
const realParseFileDiffRow = markdown.parseFileDiffRow

let chunkScans = 0
let childParses = 0

let MarkdownText: typeof import('./MarkdownText').MarkdownText

beforeAll(() => {
  mock.module('../../lib/markdown', () => ({
    ...markdown,
    parseMarkdownTable: (lines: string[]) => {
      chunkScans += 1
      return realParseMarkdownTable(lines)
    },
    parseDiffStat: (text: string) => {
      childParses += 1
      return realParseDiffStat(text)
    },
    parseFileDiffRow: (line: string) => {
      childParses += 1
      return realParseFileDiffRow(line)
    },
  }))

  MarkdownText = require('./MarkdownText').MarkdownText
})

beforeEach(() => {
  chunkScans = 0
  childParses = 0
})

afterEach(cleanup)

const SMALL = '# Title **bold**\n\n- item `one`\n\n```ts\nconst x = 1\n```\n\nPlain *em* text.\n'

const SMALL_HTML =
  '<div class="space-y-2">' +
  '<h3 class="font-semibold text-text-primary">Title <strong>bold</strong></h3>' +
  '<div class="h-2"></div>' +
  '<div class="flex gap-2 pl-4 text-sm leading-6 text-text-secondary">' +
  '<span class="mt-2 h-1.5 w-1.5 flex-shrink-0 rounded-full bg-text-tertiary"></span>' +
  '<span class="min-w-0 flex-1">item <code class="rounded bg-background-secondary px-1 py-0.5 ' +
  'font-mono text-[0.95em] text-text-primary">one</code></span></div>' +
  '<div class="h-2"></div>' +
  '<p class="text-sm leading-6 text-text-secondary whitespace-pre-wrap">```ts</p>' +
  '<p class="text-sm leading-6 text-text-secondary whitespace-pre-wrap">const x = 1</p>' +
  '<p class="text-sm leading-6 text-text-secondary whitespace-pre-wrap">```</p>' +
  '<div class="h-2"></div>' +
  '<p class="text-sm leading-6 text-text-secondary whitespace-pre-wrap">Plain <em>em</em> text.</p>' +
  '</div>'

const RICH =
  '# Title **bold**\n\n- item one\n- item `two`\n\n```ts\nconst x = 1\n```\n\n' +
  'Paragraph with [link](https://example.com) and *em*.\n\n' +
  '| a | b |\n| --- | --- |\n| 1 | 2 |\n\n3 changed files, +12 -4\n\nsrc/app.ts +5 -2\n'

const RICH_SCANS = 7

describe('MarkdownText — DOM output', () => {
  test('renders the pre-memo HTML byte-for-byte (heading, list, blank, fence, paragraph)', () => {
    const { container } = render(<MarkdownText content={SMALL} />)

    expect(container.innerHTML).toBe(SMALL_HTML)
  })

  test('trims trailing blank lines (streaming artifact) inside the same markup', () => {
    const { container } = render(<MarkdownText content={'a\n\n\n'} />)

    expect(container.innerHTML).toBe(
      '<div class="space-y-2">' +
        '<p class="text-sm leading-6 text-text-secondary whitespace-pre-wrap">a</p>' +
        '</div>',
    )
  })

  test('renders tables, diff stats and file diff rows as before', () => {
    const { container } = render(<MarkdownText content={RICH} />)

    expect(chunkScans).toBe(RICH_SCANS)

    expect(container.querySelectorAll('h3').length).toBe(1)
    expect(container.querySelectorAll('div.h-2').length).toBe(6)
    expect(container.querySelectorAll('div.flex.gap-2.pl-4').length).toBe(2)
    expect(container.querySelectorAll('table th').length).toBe(2)
    expect(container.querySelectorAll('table td').length).toBe(2)
    expect(container.textContent).toContain('3 Changed files')
    expect(container.textContent).toContain('src/app.ts')
  })

  test('renders an empty wrapper for an empty (or blank-only) message', () => {
    const { container } = render(<MarkdownText content={' \n\n'} />)

    expect(container.innerHTML).toBe('<div class="space-y-2"><div class="h-2"></div></div>')
  })
})

describe('MarkdownText — chunk memoisation', () => {
  test('re-rendering the same content does not re-run the chunk computation', () => {
    const { rerender, container } = render(<MarkdownText content={RICH} />)

    const scansAfterFirst = chunkScans
    const childParsesAfterFirst = childParses
    expect(scansAfterFirst).toBe(RICH_SCANS)
    expect(childParsesAfterFirst).toBe(2)

    for (let i = 0; i < 30; i += 1) {
      rerender(<MarkdownText content={RICH} />)
    }

    expect(chunkScans).toBe(scansAfterFirst)
    expect(childParses).toBe(childParsesAfterFirst)
    expect(container.textContent).toContain('3 Changed files')
  })

  test('changing content re-runs the chunk computation and updates the DOM', () => {
    const { rerender, container } = render(<MarkdownText content={RICH} />)

    const scansAfterFirst = chunkScans

    rerender(<MarkdownText content={`${RICH}\nappended **tail**`} />)

    expect(chunkScans).toBeGreaterThan(scansAfterFirst)
    expect(container.textContent).toContain('appended tail')
    expect(container.querySelectorAll('strong').length).toBe(2)
  })

  test('typewriter shape: one parse per distinct content, duplicates are free', () => {
    const full = `Sentence ${'x'.repeat(60)}`

    chunkScans = 0
    const baseline = render(<MarkdownText content={full} />)
    const scansPerFullRender = chunkScans
    baseline.unmount()
    cleanup()

    expect(scansPerFullRender).toBe(1)

    const versions: string[] = []
    for (let end = 4; end < full.length; end += 3) versions.push(full.slice(0, end))
    versions.push(full)

    chunkScans = 0
    let renders = 0
    const { rerender, container } = render(<MarkdownText content={versions[0]} />)
    renders += 1
    for (const version of versions.slice(1)) {
      rerender(<MarkdownText content={version} />)
      renders += 1
      rerender(<MarkdownText content={version} />)
      renders += 1
    }

    expect(renders).toBe(versions.length * 2 - 1)
    expect(chunkScans).toBe(versions.length * scansPerFullRender)
    expect(container.textContent).toBe(full)
  })

  test('trailing-blank trimming is memoised with the content', () => {
    const { rerender, container } = render(<MarkdownText content={SMALL} />)
    const scansAfterFirst = chunkScans
    const htmlAfterFirst = container.innerHTML

    for (let i = 0; i < 10; i += 1) {
      rerender(<MarkdownText content={SMALL} />)
    }

    expect(chunkScans).toBe(scansAfterFirst)
    expect(container.innerHTML).toBe(htmlAfterFirst)

    rerender(<MarkdownText content={'a\n\n\n'} />)
    const trimmedHtml = container.innerHTML
    rerender(<MarkdownText content={'a'} />)

    expect(container.innerHTML).toBe(trimmedHtml)
    expect(container.querySelectorAll('div.h-2').length).toBe(0)
  })
})
