import { describe, expect, test } from 'bun:test'
import {
  CATEGORY_ORDER,
  ESSENTIAL_TOOLS,
  KNOWN_TOOLS,
  TOOL_CATEGORIES,
  categorize,
  groupToolsByCategory,
} from './toolCatalog'

describe('categorize', () => {
  test('unknown tools fall back to other (never lose a tool)', () => {
    expect(categorize('tool_desconocida')).toBe('other')
    expect(categorize('')).toBe('other')
    expect(categorize('Read_File')).toBe('other') // case-sensitive on purpose: names come from the API
  })

  test('maps the real registered names', () => {
    expect(categorize('read_file')).toBe('files')
    expect(categorize('exec')).toBe('system')
    expect(categorize('web_fetch')).toBe('web')
    expect(categorize('spawn')).toBe('agents')
    expect(categorize('i2c')).toBe('hardware')
    expect(categorize('secret')).toBe('other')
  })
})

describe('TOOL_CATEGORIES', () => {
  test('every declared category is in CATEGORY_ORDER', () => {
    expect(Object.keys(TOOL_CATEGORIES).sort()).toEqual([...CATEGORY_ORDER].sort())
  })

  test('no tool is declared twice', () => {
    const all = CATEGORY_ORDER.flatMap((category) => TOOL_CATEGORIES[category])
    expect(new Set(all).size).toBe(all.length)
  })

  test('spec table sizes (§7.5)', () => {
    expect(TOOL_CATEGORIES.files).toHaveLength(10)
    expect(TOOL_CATEGORIES.system).toHaveLength(5)
    expect(TOOL_CATEGORIES.agents).toHaveLength(8)
    expect(TOOL_CATEGORIES.hardware).toEqual(['i2c', 'spi'])
  })
})

describe('ESSENTIAL_TOOLS', () => {
  test('all of them are categorizable (no typos against the map)', () => {
    for (const tool of ESSENTIAL_TOOLS) {
      expect(KNOWN_TOOLS).toContain(tool)
      expect(categorize(tool)).not.toBe('other')
    }
  })

  test('is the autonomous minimum set', () => {
    expect(ESSENTIAL_TOOLS).toEqual([
      'read_file',
      'write_file',
      'edit_file',
      'list_dir',
      'exec',
      'web_search',
      'web_fetch',
      'send_file',
      'sleep',
    ])
  })
})

describe('groupToolsByCategory', () => {
  test('keeps CATEGORY_ORDER and drops empty groups', () => {
    const groups = groupToolsByCategory(['web_fetch', 'exec', 'tool_desconocida'])
    expect(groups.map((group) => group.category)).toEqual(['system', 'web', 'other'])
    expect(groups[2].tools).toEqual(['tool_desconocida'])
  })

  test('empty input yields no groups', () => {
    expect(groupToolsByCategory([])).toEqual([])
  })
})
