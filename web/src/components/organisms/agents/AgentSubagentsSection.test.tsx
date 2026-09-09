import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent } from '@testing-library/react'
import type { EditableAgentConfig } from '../../../lib/types'
import {
  autoCleanup,
  type makeState,
  renderWithSettings,
  settle,
  tr,
  withModels,
} from '../../../test/agentsHarness'
import { AgentSubagentsSection } from './AgentSubagentsSection'

/**
 * `tab=subagents` (spec §4.7). The whole section writes ONE node —
 * `agents.list.{i}.subagents` — so every assertion is about the shape of that
 * object: enabling creates it, disabling removes it, and each control must
 * spread the node it replaces instead of clobbering its siblings. `0` is a real
 * value here (the backend reads it as "no limit"), never normalised away.
 */

const CODER: EditableAgentConfig = { id: 'coder', name: 'Coder' }

function setup(
  options: {
    agent?: EditableAgentConfig
    list?: EditableAgentConfig[]
    dirtyPaths?: string[]
    validationErrors?: NonNullable<Parameters<typeof makeState>[1]>['validationErrors']
  } = {},
) {
  const agent = options.agent ?? CODER
  const list = options.list ?? [agent, { id: 'researcher', name: 'Researcher' }]
  return renderWithSettings(<AgentSubagentsSection agent={agent} index={0} agents={list} />, list, {
    dirtyPaths: options.dirtyPaths,
    validationErrors: options.validationErrors,
  })
}

autoCleanup()

describe('AgentSubagentsSection', () => {
  test('an agent without the subagents node renders only the toggle', () => {
    const u = setup()
    expect(u.container.textContent).toContain(tr('settings.agentPage.subagentsEnable'))
    // Hidden while off: neither the chips group nor the limit fields exist.
    expect(u.queryByRole('group')).toBeNull()
    expect(u.queryByLabelText(tr('settings.agentPage.subagentsTimeout'))).toBeNull()
  })

  test('enabling writes an empty allowlist (nobody may be delegated to yet)', () => {
    const u = setup()
    fireEvent.click(u.getByLabelText(tr('settings.agentPage.subagentsEnable')))
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toEqual({ allow_agents: [] })
  })

  test('disabling removes the node entirely', () => {
    const u = setup({ agent: { ...CODER, subagents: { allow_agents: ['researcher'] } } })
    fireEvent.click(u.getByLabelText(tr('settings.agentPage.subagentsEnable')))
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toBeUndefined()
  })

  test('the chips list every OTHER agent, never the one being edited', () => {
    const u = setup({
      agent: { ...CODER, subagents: { allow_agents: [] } },
      list: [CODER, { id: 'researcher', name: 'Researcher' }, { id: 'fixer' }],
    })
    const group = u.getByRole('group', { name: tr('settings.agentPage.subagentsAllowed') })
    const chips = Array.from(group.querySelectorAll('button'))
    // `id (name)` when a name exists, bare `id` otherwise (AgentChipMultiSelect).
    expect(chips.map((chip) => chip.getAttribute('aria-label'))).toEqual([
      'researcher (Researcher)',
      'fixer',
    ])
    expect(u.container.textContent).not.toContain('coder (Coder)')
  })

  test('toggling a chip rewrites the node keeping the model and the limits', () => {
    const agent: EditableAgentConfig = {
      ...CODER,
      subagents: {
        allow_agents: [],
        model: { primary: 'gpt-4o' },
        timeout_minutes: 5,
        max_concurrent: 2,
        max_iterations: 30,
      },
    }
    const u = setup({ agent })
    fireEvent.click(u.getByLabelText('researcher (Researcher)'))
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toEqual({
      allow_agents: ['researcher'],
      model: { primary: 'gpt-4o' },
      timeout_minutes: 5,
      max_concurrent: 2,
      max_iterations: 30,
    })
  })

  test('a selected chip can be unselected (writes the filtered list)', () => {
    const u = setup({ agent: { ...CODER, subagents: { allow_agents: ['researcher'] } } })
    fireEvent.click(u.getByLabelText('researcher (Researcher)'))
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toMatchObject({ allow_agents: [] })
  })

  test('an unset limit displays as 0', () => {
    const u = setup({ agent: { ...CODER, subagents: { allow_agents: [] } } })
    const timeout = u.getByLabelText(tr('settings.agentPage.subagentsTimeout')) as HTMLInputElement
    expect(timeout.value).toBe('0')
  })

  test('clearing a limit to 0 writes 0 — never undefined — and keeps its siblings', () => {
    const agent: EditableAgentConfig = {
      ...CODER,
      subagents: { allow_agents: ['researcher'], timeout_minutes: 15, max_concurrent: 7 },
    }
    const u = setup({ agent })
    const timeout = u.getByLabelText(tr('settings.agentPage.subagentsTimeout')) as HTMLInputElement
    expect(timeout.value).toBe('15')

    fireEvent.change(timeout, { target: { value: '0' } })
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toEqual({
      allow_agents: ['researcher'],
      timeout_minutes: 0,
      max_concurrent: 7,
    })
  })

  test('the three limits are independent fields of the same node', () => {
    const agent: EditableAgentConfig = {
      ...CODER,
      subagents: { allow_agents: [], max_concurrent: 4 },
    }
    const u = setup({ agent })
    fireEvent.change(u.getByLabelText(tr('settings.agentPage.subagentsIterations')), {
      target: { value: '12' },
    })
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toEqual({
      allow_agents: [],
      max_concurrent: 4,
      max_iterations: 12,
    })
  })

  test('with no other agent, the chips area shows the dashed empty block', () => {
    const u = setup({
      agent: { ...CODER, subagents: { allow_agents: [] } },
      list: [CODER],
    })
    const empty = u.getByTestId('agents.list.0.subagents-empty')
    expect(empty).toBeTruthy()
    expect(empty.textContent).toContain(tr('settings.agentPage.noOtherAgents'))
  })

  test('an empty subagent model inherits: picking none writes undefined, not {}', async () => {
    withModels(['gpt-4o', 'llama-3.1'])
    const u = setup({ agent: { ...CODER, subagents: { allow_agents: [] } } })
    await settle()
    const trigger = u.getByLabelText(tr('settings.agentPage.subagentsModel'))
    expect(trigger.textContent).toContain(tr('settings.agentPage.inherit'))

    fireEvent.click(trigger)
    await settle(250) // the popup opens on an animation frame
    fireEvent.click(u.getByRole('button', { name: 'llama-3.1' }))
    expect(u.lastWrite('agents.list.0.subagents')?.[1]).toEqual({
      allow_agents: [],
      model: { primary: 'llama-3.1' },
    })
  })

  test('a dirty subagent path marks the section (§5.3)', () => {
    const u = setup({
      agent: { ...CODER, subagents: { allow_agents: [] } },
      dirtyPaths: ['agents.list.0.subagents.allow_agents'],
    })
    // SettingsField renders the shared "Modified" badge next to the label.
    expect(u.container.textContent).toContain(tr('settings.modified'))
  })

  test('a validation error on a subagent subpath surfaces under that field (§5.4)', () => {
    const u = setup({
      agent: { ...CODER, subagents: { allow_agents: [] } },
      validationErrors: [
        {
          path: 'agents.list.0.subagents.timeout_minutes',
          message: 'too big',
          code: 'invalid',
        },
      ],
    })
    expect(u.container.textContent).toContain('too big')
  })
})
