import { describe, expect, test } from 'bun:test'
import {
  AGENT_TABS,
  SECTION_PATHS,
  isAgentDirty,
  isAgentTab,
  isSectionDirty,
  sectionPrefixes,
} from './agentDirty'

const dirty = (...paths: string[]) => new Set(paths)

describe('SECTION_PATHS (spec §5.3 table)', () => {
  test('covers every tab and nothing outside the agent list', () => {
    expect(Object.keys(SECTION_PATHS).sort()).toEqual([...AGENT_TABS].sort())
    for (const tab of AGENT_TABS) {
      for (const suffix of SECTION_PATHS[tab]) {
        expect(suffix.startsWith('agents')).toBe(false)
      }
    }
  })

  test('files never writes config', () => {
    expect(SECTION_PATHS.files).toEqual([])
    expect(sectionPrefixes(2, 'files')).toEqual([])
  })
})

describe('isSectionDirty', () => {
  test('nested paths light up their section (…model.primary -> model)', () => {
    const paths = dirty('agents.list.0.model.primary')
    expect(isSectionDirty(paths, 0, 'model')).toBe(true)
    expect(isSectionDirty(paths, 0, 'general')).toBe(false)
    expect(isSectionDirty(paths, 1, 'model')).toBe(false)
  })

  test('the section prefix itself counts, not only children', () => {
    expect(isSectionDirty(dirty('agents.list.3.subagents'), 3, 'subagents')).toBe(true)
  })

  test('general lists exactly name/description/default/workspace', () => {
    for (const suffix of ['name', 'description', 'default', 'workspace']) {
      expect(isSectionDirty(dirty(`agents.list.1.${suffix}`), 1, 'general')).toBe(true)
    }
    // temperature is the model tab's, not general's
    expect(isSectionDirty(dirty('agents.list.1.temperature'), 1, 'general')).toBe(false)
    expect(isSectionDirty(dirty('agents.list.1.temperature'), 1, 'model')).toBe(true)
  })

  test('sibling index is never matched by prefix', () => {
    // agents.list.1.model must not light up agent 12.
    expect(isSectionDirty(dirty('agents.list.1.model'), 12, 'model')).toBe(false)
    expect(isSectionDirty(dirty('agents.list.12.model'), 1, 'model')).toBe(false)
  })

  test('array-index children also match', () => {
    expect(isSectionDirty(dirty('agents.list.0.model.fallbacks[1]'), 0, 'model')).toBe(true)
  })

  test('skills/tools/subagents each own their prefix', () => {
    expect(isSectionDirty(dirty('agents.list.2.skills'), 2, 'skills')).toBe(true)
    expect(isSectionDirty(dirty('agents.list.2.skills'), 2, 'tools')).toBe(false)
    expect(isSectionDirty(dirty('agents.list.2.tools[0]'), 2, 'tools')).toBe(true)
  })

  test('files is always clean, even with every field dirty', () => {
    const paths = dirty('agents.list.0.name', 'agents.list.0.model', 'agents.list.0.tools')
    expect(isSectionDirty(paths, 0, 'files')).toBe(false)
  })

  test('empty set is clean everywhere', () => {
    for (const tab of AGENT_TABS) {
      expect(isSectionDirty(new Set(), 0, tab)).toBe(false)
    }
  })
})

describe('isAgentDirty', () => {
  test('any field of the agent marks the card', () => {
    expect(isAgentDirty(dirty('agents.list.4.workspace'), 4)).toBe(true)
    expect(isAgentDirty(dirty('agents.list.4.workspace'), 5)).toBe(false)
    expect(isAgentDirty(dirty('agents.defaults.model'), 0)).toBe(false)
  })
})

describe('isAgentTab', () => {
  test('accepts the six known tabs and rejects anything else', () => {
    for (const tab of AGENT_TABS) expect(isAgentTab(tab)).toBe(true)
    expect(isAgentTab('identity')).toBe(false)
    expect(isAgentTab(undefined)).toBe(false)
  })
})
