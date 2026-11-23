import { describe, expect, test } from 'bun:test'
import { defaultAgentId, selectableAgents } from './agentSelection'
import type { EditableAgentConfig } from './types'

const CODER: EditableAgentConfig = { id: 'coder', name: 'Coder' }
const RESEARCHER: EditableAgentConfig = { id: 'researcher', name: 'Researcher', default: true }

describe('selectableAgents', () => {
  test('keeps the list order and the position of each agent in the original list', () => {
    const blank: EditableAgentConfig = { id: '   ' }
    const selectable = selectableAgents([blank, CODER])
    expect(selectable.map((entry) => entry.id)).toEqual(['coder'])
    expect(selectable[0].index).toBe(1)
    expect(selectable[0].agent).toBe(CODER)
  })

  test('exposes the trimmed id while leaving the agent entry untouched', () => {
    const padded: EditableAgentConfig = { id: ' coder ' }
    const selectable = selectableAgents([padded])
    expect(selectable[0].id).toBe('coder')
    expect(selectable[0].agent.id).toBe(' coder ')
  })

  test('drops empty and whitespace-only ids', () => {
    expect(selectableAgents([{ id: '' }, { id: '   ' }, { id: '\t' }])).toEqual([])
  })

  test('a list without blanks is unchanged', () => {
    const selectable = selectableAgents([CODER, RESEARCHER])
    expect(selectable.map((entry) => entry.id)).toEqual(['coder', 'researcher'])
    expect(selectable.map((entry) => entry.index)).toEqual([0, 1])
  })
})

describe('defaultAgentId', () => {
  test('prefers the agent marked default', () => {
    expect(defaultAgentId(selectableAgents([CODER, RESEARCHER]))).toBe('researcher')
  })

  test('skips a blank entry even when it is marked default', () => {
    expect(defaultAgentId(selectableAgents([{ id: '  ', default: true }, CODER]))).toBe('coder')
  })

  test('falls back to the first usable agent when none is marked default', () => {
    expect(defaultAgentId(selectableAgents([{ id: ' ' }, CODER]))).toBe('coder')
  })

  test('returns null when every id is blank', () => {
    expect(defaultAgentId(selectableAgents([{ id: ' ', default: true }]))).toBeNull()
  })

  test('returns null for an empty list', () => {
    expect(defaultAgentId(selectableAgents([]))).toBeNull()
  })
})
