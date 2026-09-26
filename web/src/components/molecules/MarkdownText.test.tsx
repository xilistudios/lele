import { afterEach, beforeAll, beforeEach, describe, expect, mock, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import * as markdown from '../../lib/markdown'

/**
 * MarkdownText parses the WHOLE message on every render unless the chunk memo
 * hits. These tests pin that behaviour down with two independent witnesses:
 *
 *  - `chunkScans`: `parseMarkdownTable` is called inside the chunk memo once
 *    per line that is not blank/heading/list, so its call count is an exact
 *    proxy for "the chunk computation ran". It must NOT grow when `content`
 *    is unchanged.
 *  - `childParses`: `DiffStat`/`FileDiffRow` call `parseDiffStat` /
 *    `parseFileDiffRow` in their render bodies (no memo of their own), so when
 *    the memo hits, React reuses the very same element objects and bails out
 *    of re-rendering that subtree — `childParses` must stay flat too.
 */

// Captured before mock.module() runs so the wrapper can never call itself.
const realParseMarkdownTable = markdown.parseMarkdownTable
const realParseDiffStat = markdown.parseDiffStat
const realParseFileDiffRow = markdown.parseFileDiffRow

let chunkScans = 0
let childParses = 0

// Deferred import: resolved after mock.module() so the component picks the
// instrumented markdown module up.
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

/** Heading + list + blank line + code fence + plain paragraph. */
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

/** Adds table + diff-stat + file-diff rows on top of SMALL. */
const RICH =
  '# Title **bold**\n\n- item one\n- item `two`\n\n```ts\nconst x = 1\n```\n\n' +
  'Paragraph with [link](https://example.com) and *em*.\n\n' +
  '| a | b |\n| --- | --- |\n| 1 | 2 |\n\n3 changed files, +12 -4\n\nsrc/app.ts +5 -2\n'

/** `parseMarkdownTable` calls for one full computation of RICH (see comment). */
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

    // every non-blank, non-heading, non-list line produces a scan
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

    // 30 extra renders of an unchanged message → still exactly ONE parse.
    expect(chunkScans).toBe(scansAfterFirst)
    // …and React even skips re-rendering the per-line children.
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

    // Every distinct content value the typewriter can produce, ending with the
    // complete message.
    const versions: string[] = []
    for (let end = 4; end < full.length; end += 3) versions.push(full.slice(0, end))
    versions.push(full)

    chunkScans = 0
    let renders = 0
    const { rerender, container } = render(<MarkdownText content={versions[0]} />)
    renders += 1
    for (const version of versions.slice(1)) {
      // the typewriter tick itself…
      rerender(<MarkdownText content={version} />)
      renders += 1
      // …plus an unrelated parent re-render with identical content.
      rerender(<MarkdownText content={version} />)
      renders += 1
    }

    // 2 renders per version (-1 for the initial one) but only 1 parse per version.
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

    // 'a\n\n\n' trims down to the same markup as 'a' (the artifact newlines are
    // dropped inside the memo, so no trailing blank spacer is emitted).
    rerender(<MarkdownText content={'a\n\n\n'} />)
    const trimmedHtml = container.innerHTML
    rerender(<MarkdownText content={'a'} />)

    expect(container.innerHTML).toBe(trimmedHtml)
    expect(container.querySelectorAll('div.h-2').length).toBe(0)
  })
})
