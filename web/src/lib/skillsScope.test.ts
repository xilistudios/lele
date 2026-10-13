import { describe, expect, test } from 'bun:test'
import {
  AGENT_PARAM,
  GLOBAL_VIEW_SOURCES,
  SKILLS_VIEWS,
  VIEW_PARAM,
  catalogEntryToSkillInfo,
  filterSkillsForView,
  isInGlobalView,
  isSkillsView,
  parseSkillsView,
  resolveAgentId,
  viewHintKey,
} from './skillsScope'
import type { Agent, AgentSkillCatalogEntry, SkillInfo } from './types'

const skill = (over: Partial<SkillInfo>): SkillInfo => ({
  id: 'x',
  name: 'x',
  description: '',
  installed: true,
  enabled: true,
  ...over,
})

const agent = (over: Partial<Agent>): Agent => ({
  id: 'a',
  name: 'A',
  workspace: '/tmp/a',
  model: 'm',
  ...over,
})

describe('skillsScope constants', () => {
  test('the tab order and URL params are the ones the page writes (D1)', () => {
    expect(SKILLS_VIEWS).toEqual(['all', 'global', 'agent'])
    expect(VIEW_PARAM).toBe('view')
    expect(AGENT_PARAM).toBe('agent')
  })
})

describe('parseSkillsView', () => {
  test('known views pass through', () => {
    expect(parseSkillsView('all')).toBe('all')
    expect(parseSkillsView('global')).toBe('global')
    expect(parseSkillsView('agent')).toBe('agent')
  })

  test('anything unknown normalises to all', () => {
    expect(parseSkillsView(null)).toBe('all')
    expect(parseSkillsView(undefined)).toBe('all')
    expect(parseSkillsView('')).toBe('all')
    expect(parseSkillsView('GLOBAL')).toBe('all') // case-sensitive on purpose
    expect(parseSkillsView('skills')).toBe('all')
  })

  test('isSkillsView narrows only the three known values', () => {
    expect(isSkillsView('global')).toBe(true)
    expect(isSkillsView('nope')).toBe(false)
    expect(isSkillsView(null)).toBe(false)
  })
})

describe('GLOBAL_VIEW_SOURCES / isInGlobalView (D2)', () => {
  test("GLOBAL_VIEW_SOURCES is exactly ['global'] — built-ins are excluded by design", () => {
    // D2: the Global tab is not only a filter, it selects the install
    // destination (`scope:"global"` ⇒ <leleDir>/skills). Built-ins live in
    // <leleDir>/lele/skills, can never be installed to or removed from the
    // global dir, and are already read-only on this page. Think twice before
    // changing this line: it is a decision, not an oversight.
    expect(GLOBAL_VIEW_SOURCES).toEqual(['global'])
  })

  test('global skills are in the view; workspace, builtin and unknown are not', () => {
    expect(isInGlobalView(skill({ source: 'global' }))).toBe(true)
    expect(isInGlobalView(skill({ source: 'workspace' }))).toBe(false)
    expect(isInGlobalView(skill({ source: 'builtin' }))).toBe(false)
    expect(isInGlobalView({ source: undefined })).toBe(false)
  })
})

describe('filterSkillsForView', () => {
  const skills = [
    skill({ id: 'w', name: 'w', source: 'workspace' }),
    skill({ id: 'g1', name: 'g1', source: 'global' }),
    skill({ id: 'b', name: 'b', source: 'builtin' }),
    skill({ id: 'g2', name: 'g2', source: 'global' }),
  ]

  test('all returns the very same array (identity, no copy)', () => {
    expect(filterSkillsForView(skills, 'all')).toBe(skills)
  })

  test('global keeps only the global skills, in order', () => {
    expect(filterSkillsForView(skills, 'global').map((s) => s.name)).toEqual(['g1', 'g2'])
  })
})

describe('catalogEntryToSkillInfo', () => {
  const entry: AgentSkillCatalogEntry = {
    name: 'weather',
    description: 'Get the weather',
    source: 'workspace',
    enabled: false,
    deletable: true,
  }

  test('maps the catalog entry onto SkillInfo (id = name, installed = true)', () => {
    expect(catalogEntryToSkillInfo(entry)).toEqual({
      id: 'weather',
      name: 'weather',
      description: 'Get the weather',
      installed: true,
      enabled: false,
      source: 'workspace',
    })
  })

  test('does not leak the server-only deletable flag onto SkillInfo', () => {
    expect('deletable' in catalogEntryToSkillInfo(entry)).toBe(false)
  })
})

describe('resolveAgentId (D11)', () => {
  const coder = agent({ id: 'coder', name: 'Coder', default: true })
  const planner = agent({ id: 'planner', name: 'Planner' })
  const agents = [coder, planner]

  test('1. a requested id that exists wins, even over the current agent', () => {
    expect(resolveAgentId('planner', agents, coder)).toBe('planner')
  })

  test('a requested id NOT in the list falls through to the current agent', () => {
    expect(resolveAgentId('ghost', agents, planner)).toBe('planner')
  })

  test('2. with no requested id, the current agent of the chat wins', () => {
    expect(resolveAgentId(null, agents, planner)).toBe('planner')
  })

  test('3. with no current agent, the default agent wins', () => {
    expect(resolveAgentId(null, agents, null)).toBe('coder')
  })

  test('4. with no default, the first agent wins', () => {
    const noDefault = [agent({ id: 'a' }), agent({ id: 'b' })]
    expect(resolveAgentId(null, noDefault, null)).toBe('a')
  })

  test('5. no agents at all resolves to null', () => {
    expect(resolveAgentId(null, [], null)).toBeNull()
    expect(resolveAgentId('ghost', [], null)).toBeNull()
  })
})

describe('viewHintKey', () => {
  test('one hint key per view', () => {
    expect(viewHintKey('all')).toBe('skills.viewAllHint')
    expect(viewHintKey('global')).toBe('skills.viewGlobalHint')
    expect(viewHintKey('agent')).toBe('skills.viewAgentHint')
  })
})
