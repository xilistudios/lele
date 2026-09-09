import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent } from '@testing-library/react'
import type { EditableAgentConfig } from '../../../lib/types'
import { autoCleanup, renderWithSettings, tr } from '../../../test/agentsHarness'
import { AgentGeneralSection } from './AgentGeneralSection'

/**
 * `tab=general` (spec §4.3). Rendered through the shared SettingsProvider
 * harness so `updateField` calls can be asserted: the paths written are the
 * contract with `lib/agentDirty.ts` and the backend.
 */

const coder: EditableAgentConfig = { id: 'coder', name: 'Coder' }
const INDEX = 1

function setup(
  agent: EditableAgentConfig = coder,
  agents: EditableAgentConfig[] = [{ id: 'main', default: true }, agent],
  options: Parameters<typeof renderWithSettings>[2] = {},
) {
  return renderWithSettings(
    <AgentGeneralSection agent={agent} index={INDEX} agents={agents} />,
    agents,
    options,
  )
}

autoCleanup()

describe('AgentGeneralSection', () => {
  test('renders the five identity fields', () => {
    const utils = setup()
    for (const label of [
      'ID',
      tr('settings.fields.agentName'),
      tr('settings.fields.agentDescription'),
      tr('settings.fields.agentDefault'),
      tr('settings.fields.agentWorkspace'),
    ]) {
      expect(utils.getByText(label)).toBeTruthy()
    }
  })

  test('ID is read-only and shows the value', () => {
    const utils = setup()
    const field = utils.getByTestId('agent-id-field')
    expect(field.getAttribute('role')).toBe('textbox')
    expect(field.getAttribute('aria-readonly')).toBe('true')
    expect(field.textContent).toBe('coder')
    // Not a real input: it can never be typed into.
    expect(field.tagName).toBe('DIV')
  })

  test('the copy button never throws when the clipboard is unavailable', async () => {
    const utils = setup()
    const button = utils.getByTitle(tr('settings.agentPage.copyId'))
    fireEvent.click(button)
    // jsdom has no navigator.clipboard: the fallback path must still confirm.
    await Promise.resolve()
    expect(utils.getByTestId('agent-id-field').textContent).toBe('coder')
  })

  test('name writes undefined for an empty string (never "")', () => {
    const utils = setup()
    const input = utils.getByLabelText(tr('settings.fields.agentName'))
    fireEvent.change(input, { target: { value: 'Senior Coder' } })
    expect(utils.lastWrite(`agents.list.${INDEX}.name`)?.[1]).toBe('Senior Coder')
    fireEvent.change(input, { target: { value: '' } })
    expect(utils.lastWrite(`agents.list.${INDEX}.name`)?.[1]).toBe(undefined)
  })

  test('description writes its own path', () => {
    const utils = setup()
    fireEvent.change(utils.getByLabelText(tr('settings.fields.agentDescription')), {
      target: { value: 'writes code' },
    })
    expect(utils.lastWrite(`agents.list.${INDEX}.description`)).toEqual([
      `agents.list.${INDEX}.description`,
      'writes code',
    ])
  })

  test('default checkbox toggles the boolean', () => {
    const utils = setup()
    const checkbox = utils.getByLabelText(tr('settings.fields.agentDefault'))
    fireEvent.click(checkbox)
    expect(utils.lastWrite(`agents.list.${INDEX}.default`)?.[1]).toBe(true)
  })

  test('warning when several agents are default (§4.3)', () => {
    const agents = [
      { id: 'main', default: true },
      { id: 'coder', default: true },
    ]
    const utils = setup(agents[1], agents)
    expect(utils.getByTestId('multiple-defaults-warning').textContent).toBe(
      tr('settings.agentPage.multipleDefaults'),
    )
  })

  test('no warning with a single default', () => {
    expect(setup().queryByTestId('multiple-defaults-warning')).toBeNull()
  })

  test('workspace shows the ~ preview only when the value has a ~', () => {
    const withTilde = setup({ id: 'coder', workspace: '~/.lele/workspace-coder' })
    expect(withTilde.getByTestId('workspace-preview').textContent).toBe('~/.lele/workspace-coder')
    withTilde.unmount()
    const absolute = setup({ id: 'coder', workspace: '/home/alfredo/wk' })
    expect(absolute.queryByTestId('workspace-preview')).toBeNull()
  })

  test('modified badge appears only on the dirty field (§5.3)', () => {
    const utils = setup(coder, [coder], { dirtyPaths: [`agents.list.${INDEX}.name`] })
    const badges = utils.getAllByText(tr('settings.modified'))
    expect(badges).toHaveLength(1)
  })

  test('inline validation error under the failing field (§5.4)', () => {
    const utils = setup(coder, [coder], {
      validationErrors: [
        { path: `agents.list.${INDEX}.name`, message: 'name is required', code: 'invalid' },
      ],
    })
    expect(utils.getByText('name is required')).toBeTruthy()
  })

  test('an error on a sibling agent does not leak into this form', () => {
    const utils = setup(coder, [coder], {
      validationErrors: [{ path: 'agents.list.9.name', message: 'not mine', code: 'invalid' }],
    })
    expect(utils.queryByText('not mine')).toBeNull()
  })
})
