import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent } from '@testing-library/react'
import type { EditableAgentConfig } from '../../../lib/types'
import {
  autoCleanup,
  renderWithSettings,
  settle,
  tr,
  withModels,
} from '../../../test/agentsHarness'
import { AgentModelSection } from './AgentModelSection'

/**
 * `tab=model` (spec §4.4). The contract under test is what each control
 * writes: model must keep the sibling field it does not touch, thinking `''`
 * must become `undefined`, and the temperature reset must mean "inherit".
 */

const INDEX = 2

function setup(agent: EditableAgentConfig, defaultsModel = 'gemini-2.5-pro') {
  const agents = [{ id: 'main' }, { id: 'researcher' }, agent]
  return renderWithSettings(<AgentModelSection agent={agent} index={INDEX} />, agents, {
    defaultsModel,
  })
}

autoCleanup()

describe('AgentModelSection', () => {
  test('renders the two sections: Model and Reasoning (§4.4)', () => {
    const utils = setup({ id: 'coder' })
    expect(utils.getByText(tr('settings.sections.model'))).toBeTruthy()
    expect(utils.getByText(tr('settings.agentPage.reasoningSection'))).toBeTruthy()
  })

  test('choosing a primary model keeps the existing fallbacks (§4.4)', async () => {
    withModels(['gpt-4o', 'llama-3.1'])
    const utils = setup({ id: 'coder', model: { primary: 'claude-sonnet-4', fallbacks: ['x'] } })
    await settle()
    fireEvent.click(utils.getByLabelText(tr('settings.fields.agentModelPrimary')))
    await settle(250) // the popup opens on an animation frame
    fireEvent.click(utils.getByRole('button', { name: 'llama-3.1' }))
    expect(utils.lastWrite(`agents.list.${INDEX}.model`)?.[1]).toEqual({
      primary: 'llama-3.1',
      fallbacks: ['x'],
    })
    withModels([])
  })

  test('adding a fallback writes the model object with the new list', async () => {
    withModels(['gpt-4o', 'llama-3.1'])
    const utils = setup({ id: 'coder', model: { primary: 'claude-sonnet-4' } })
    await settle()
    // Fallbacks use StringListEditor, whose trigger is labelled by its path id.
    fireEvent.click(utils.getByLabelText(`agents.list.${INDEX}.model.fallbacks`))
    await settle(250)
    fireEvent.click(utils.getByRole('button', { name: 'gpt-4o' }))
    const write = utils.lastWrite(`agents.list.${INDEX}.model`)
    expect(write?.[1]).toEqual({ primary: 'claude-sonnet-4', fallbacks: ['gpt-4o'] })
    withModels([])
  })

  test('thinking level: 5 segments, "inherit" first (§4.4)', () => {
    const utils = setup({ id: 'coder' })
    const radios = utils.getAllByRole('radio')
    expect(radios).toHaveLength(5)
    expect(radios[0].textContent).toBe(tr('settings.options.thinkingInherit'))
  })

  test('thinking level marks the stored value checked', () => {
    const utils = setup({ id: 'coder', thinking_level: 'high' })
    const checked = utils
      .getAllByRole('radio')
      .filter((r) => r.getAttribute('aria-checked') === 'true')
    expect(checked).toHaveLength(1)
    expect(checked[0].textContent).toBe(tr('settings.options.thinkingHigh'))
  })

  test('choosing a thinking level writes it', () => {
    const utils = setup({ id: 'coder' })
    fireEvent.click(utils.getAllByRole('radio')[4]) // high
    expect(utils.lastWrite(`agents.list.${INDEX}.thinking_level`)?.[1]).toBe('high')
  })

  test('choosing "inherit" writes undefined, not "" (§4.4 sentinel)', () => {
    const utils = setup({ id: 'coder', thinking_level: 'low' })
    fireEvent.click(utils.getAllByRole('radio')[0])
    const write = utils.lastWrite(`agents.list.${INDEX}.thinking_level`)
    expect(write?.[1]).toBe(undefined)
  })

  test('temperature slider writes numbers between 0 and 2', () => {
    const utils = setup({ id: 'coder', temperature: 0.7 })
    const slider = utils.getByRole('slider')
    fireEvent.change(slider, { target: { value: '1.4' } })
    expect(utils.lastWrite(`agents.list.${INDEX}.temperature`)?.[1]).toBe(1.4)
  })

  test('an unset temperature reports 0.7 as inherited value (§4.4)', () => {
    const utils = setup({ id: 'coder' })
    const slider = utils.getByRole('slider')
    expect(slider.getAttribute('aria-valuenow')).toBe('0.7')
    expect(slider.getAttribute('aria-valuetext')).toContain(tr('settings.agentPage.tempNotSet'))
  })

  test('resetting the temperature writes undefined = inherit (not 0.7)', () => {
    const utils = setup({ id: 'coder', temperature: 1.2 })
    fireEvent.click(utils.getByLabelText(tr('settings.agentPage.tempReset')))
    const write = utils.lastWrite(`agents.list.${INDEX}.temperature`)
    expect(write?.[1]).toBe(undefined)
  })

  test('modified badge on the changed field only (§5.3)', () => {
    const utils = renderWithSettings(
      <AgentModelSection agent={{ id: 'coder' }} index={INDEX} />,
      [{ id: 'coder' }],
      {
        dirtyPaths: [`agents.list.${INDEX}.temperature`],
      },
    )
    expect(utils.getAllByText(tr('settings.modified'))).toHaveLength(1)
  })

  test('validation error renders under the offending field (§5.4)', () => {
    const utils = renderWithSettings(
      <AgentModelSection agent={{ id: 'coder' }} index={INDEX} />,
      [{ id: 'coder' }],
      {
        validationErrors: [
          { path: `agents.list.${INDEX}.model.primary`, message: 'unknown model', code: 'invalid' },
        ],
      },
    )
    expect(utils.getByText('unknown model')).toBeTruthy()
  })
})
