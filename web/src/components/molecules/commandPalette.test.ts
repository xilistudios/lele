import { describe, expect, test } from 'bun:test'
import type { SlashCommandInfo } from '../../lib/types'
import { completeDraft, filterCommands, isPaletteTrigger } from './commandPalette'

const clear: SlashCommandInfo = {
  name: '/clear',
  description: 'Clear the conversation history for this session.',
  usage: '/clear',
}
const compact: SlashCommandInfo = {
  name: '/compact',
  description: 'Summarize and compact the conversation history (needs 5+ messages).',
  usage: '/compact',
}
const commands: SlashCommandInfo[] = [clear, compact]

describe('isPaletteTrigger', () => {
  test('bare "/" triggers the palette', () => {
    expect(isPaletteTrigger('/')).toBe(true)
  })

  test('partial command name triggers', () => {
    expect(isPaletteTrigger('/cl')).toBe(true)
  })

  test('exact command name still triggers', () => {
    expect(isPaletteTrigger('/clear')).toBe(true)
  })

  test('command with args does not trigger', () => {
    expect(isPaletteTrigger('/clear foo')).toBe(false)
  })

  test('slash followed by whitespace does not trigger', () => {
    expect(isPaletteTrigger('/ ')).toBe(false)
    expect(isPaletteTrigger('/\t')).toBe(false)
  })

  test('plain text does not trigger', () => {
    expect(isPaletteTrigger('hello')).toBe(false)
    expect(isPaletteTrigger('')).toBe(false)
  })

  test('slash in the middle of the text does not trigger', () => {
    expect(isPaletteTrigger('see /clear')).toBe(false)
  })
})

describe('filterCommands', () => {
  test('matches by case-insensitive prefix', () => {
    expect(filterCommands(commands, '/CL')).toEqual([clear])
    expect(filterCommands(commands, '/co')).toEqual([compact])
  })

  test('bare "/" returns every command in backend order', () => {
    expect(filterCommands(commands, '/')).toEqual(commands)
  })

  test('exact name keeps the single match visible', () => {
    expect(filterCommands(commands, '/clear')).toEqual([clear])
  })

  test('empty query string returns nothing', () => {
    expect(filterCommands(commands, '')).toEqual([])
  })

  test('hides the menu once a command is being used with args', () => {
    expect(filterCommands(commands, '/clear foo')).toEqual([])
    expect(filterCommands(commands, '/ ')).toEqual([])
  })

  test('non-trigger text returns nothing', () => {
    expect(filterCommands(commands, 'hello')).toEqual([])
  })

  test('no match yields empty items', () => {
    expect(filterCommands(commands, '/zzz')).toEqual([])
  })

  test('empty command list yields empty items', () => {
    expect(filterCommands([], '/')).toEqual([])
  })
})

describe('completeDraft', () => {
  test('accepting a command replaces the draft with name + trailing space', () => {
    expect(completeDraft('/cl', clear)).toBe('/clear ')
    expect(completeDraft('/compact', compact)).toBe('/compact ')
  })

  test('completion is a trigger with args ready (palette hides via the space)', () => {
    expect(isPaletteTrigger(completeDraft('/c', clear))).toBe(false)
  })
})

// ── Custom (harness-defined) commands ───────────────────────────────────────
// The backend now merges user-defined commands into /api/v1/chat/commands with
// the same shape plus `source`. Nothing may filter them out: the palette is
// their only discovery surface.

const review: SlashCommandInfo = {
  name: '/review',
  description: 'Review the current diff',
  usage: '/review [args]',
  source: 'workspace',
}
const hola: SlashCommandInfo = {
  name: '/hola',
  description: 'Saluda',
  usage: '/hola [args]',
  source: 'directory',
}

describe('custom commands in the palette', () => {
  test('custom entries survive filtering (no whitelist on name/source)', () => {
    const all = [...commands, review, hola]
    expect(filterCommands(all, '/r')).toEqual([review])
    expect(filterCommands(all, '/')).toEqual(all)
  })

  test('unknown source values still render (source is display-only)', () => {
    const exotic: SlashCommandInfo = { ...review, source: 'future-level' }
    expect(filterCommands([...commands, exotic], '/rev')).toEqual([exotic])
  })

  test('accepting a custom command leaves a trailing space for args', () => {
    expect(completeDraft('/rev', review)).toBe('/review ')
    expect(completeDraft('/h', hola)).toBe('/hola ')
  })

  test('the completed custom draft hides the palette and is ready for args', () => {
    const draft = completeDraft('/rev', review)
    expect(isPaletteTrigger(draft)).toBe(false)
    // Typing args after the completion keeps the command in use.
    expect(filterCommands([...commands, review], `${draft}src/main.go`)).toEqual([])
  })
})
