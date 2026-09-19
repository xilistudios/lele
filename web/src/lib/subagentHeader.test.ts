import { describe, expect, test } from 'bun:test'
import {
  resolveHeaderAgentName,
  resolveHeaderTitle,
  selectSubagentForSession,
} from './subagentHeader'
import type { Agent, SubagentTaskInfo } from './types'

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const makeSubagent = (overrides: Partial<SubagentTaskInfo> = {}): SubagentTaskInfo => ({
  task_id: 't-1',
  session_key: 'abc-123:subagent-1',
  label: 'Fix build',
  agent_id: 'coder',
  status: 'running',
  summary: '',
  created: Date.now(),
  updated: Date.now(),
  iterations: 1,
  ...overrides,
})

const coderAgent: Agent = {
  id: 'coder',
  name: 'Software Engineer Agent',
  workspace: '/tmp',
  model: 'claude-sonnet',
}

const reviewerAgent: Agent = {
  id: 'reviewer',
  name: 'Code Reviewer',
  workspace: '/tmp',
  model: 'claude-sonnet',
}

// ---------------------------------------------------------------------------
// selectSubagentForSession
// ---------------------------------------------------------------------------

describe('selectSubagentForSession', () => {
  test('returns matching entry by exact session_key', () => {
    const entry = makeSubagent({ session_key: 'abc:subagent-1' })
    expect(selectSubagentForSession([entry], 'abc:subagent-1')).toBe(entry)
  })

  test('returns null when sessionKey is null', () => {
    const entry = makeSubagent()
    expect(selectSubagentForSession([entry], null)).toBeNull()
  })

  test('returns null when sessionKey is empty string', () => {
    const entry = makeSubagent()
    expect(selectSubagentForSession([entry], '')).toBeNull()
  })

  test('returns null when no entry matches', () => {
    const entry = makeSubagent({ session_key: 'abc:subagent-1' })
    expect(selectSubagentForSession([entry], 'xyz:subagent-2')).toBeNull()
  })

  test('returns null when list is empty', () => {
    expect(selectSubagentForSession([], 'abc:subagent-1')).toBeNull()
  })

  test('matches among multiple entries', () => {
    const a = makeSubagent({ session_key: 'p:subagent-1', label: 'First' })
    const b = makeSubagent({ session_key: 'p:subagent-2', label: 'Second' })
    const c = makeSubagent({ session_key: 'p:subagent-3', label: 'Third' })
    expect(selectSubagentForSession([a, b, c], 'p:subagent-2')).toBe(b)
  })
})

// ---------------------------------------------------------------------------
// resolveHeaderTitle
// ---------------------------------------------------------------------------

describe('resolveHeaderTitle', () => {
  test('returns trimmed label when non-empty', () => {
    const entry = makeSubagent({ label: '  Fix build  ' })
    expect(resolveHeaderTitle(entry, 'fallback')).toBe('Fix build')
  })

  test('falls back when label is empty', () => {
    const entry = makeSubagent({ label: '' })
    expect(resolveHeaderTitle(entry, 'fallback title')).toBe('fallback title')
  })

  test('falls back when label is whitespace-only', () => {
    const entry = makeSubagent({ label: '   ' })
    expect(resolveHeaderTitle(entry, 'fallback title')).toBe('fallback title')
  })

  test('falls back when entry is null', () => {
    expect(resolveHeaderTitle(null, 'fallback title')).toBe('fallback title')
  })
})

// ---------------------------------------------------------------------------
// resolveHeaderAgentName
// ---------------------------------------------------------------------------

describe('resolveHeaderAgentName', () => {
  test('returns agent name when agent_id found in agents list', () => {
    const entry = makeSubagent({ agent_id: 'coder' })
    expect(resolveHeaderAgentName(entry, [coderAgent, reviewerAgent], 'Default')).toBe(
      'Software Engineer Agent',
    )
  })

  test('returns raw agent_id when agent not found in agents list', () => {
    const entry = makeSubagent({ agent_id: 'unknown-agent' })
    expect(resolveHeaderAgentName(entry, [coderAgent], 'Default')).toBe('unknown-agent')
  })

  test('returns raw agent_id when agents list is empty', () => {
    const entry = makeSubagent({ agent_id: 'coder' })
    expect(resolveHeaderAgentName(entry, [], 'Default')).toBe('coder')
  })

  test('returns fallback when entry is null', () => {
    expect(resolveHeaderAgentName(null, [coderAgent], 'Default Agent')).toBe('Default Agent')
  })

  test('returns fallback when entry is null and agents list is empty', () => {
    expect(resolveHeaderAgentName(null, [], 'Default Agent')).toBe('Default Agent')
  })
})
