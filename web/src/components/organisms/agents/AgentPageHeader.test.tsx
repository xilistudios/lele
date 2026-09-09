import '../../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import '../../../test/i18n'
import type { EditableAgentConfig } from '../../../lib/types'
import { AgentPageHeader, CAN_OPEN_CHAT_WITH_AGENT } from './AgentPageHeader'

/**
 * AgentPageHeader (spec §4.2). Rendered inside a MemoryRouter because it uses
 * `useNavigate` and `<Link>`. `screen` is unusable in this env (its global
 * document binding is captured before JSDOM is installed), so queries come
 * from the bound helpers returned by `render`.
 */

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

function setup(
  agent: Partial<EditableAgentConfig> = {},
  props: { isModified?: boolean; defaultsModel?: string } = {},
) {
  const full: EditableAgentConfig = { id: 'coder', ...agent }
  const utils = render(
    <MemoryRouter initialEntries={['/agents/coder/model']}>
      <LocationProbe />
      <Routes>
        <Route
          path="/agents/:agentId/:tab"
          element={
            <AgentPageHeader
              agent={full}
              isModified={props.isModified ?? false}
              defaultsModel={props.defaultsModel ?? 'gemini-2.5-pro'}
            />
          }
        />
        <Route path="/agents" element={<div data-testid="list-page" />} />
      </Routes>
    </MemoryRouter>,
  )
  return { ...utils, meta: () => utils.queryByTestId('agent-header-meta') }
}

afterEach(cleanup)

describe('AgentPageHeader', () => {
  test('breadcrumb links back to the agents list', () => {
    const { getByText, getByTestId } = setup()
    const link = getByText('Agentes')
    expect(link.closest('a')).toBeTruthy()
    fireEvent.click(link)
    expect(getByTestId('list-page')).toBeTruthy()
    expect(getByTestId('location').textContent).toBe('/agents')
  })

  test('back arrow navigates to /agents (draft survives: the provider is in the parent)', () => {
    const { getByLabelText, getByTestId } = setup()
    fireEvent.click(getByLabelText('Agentes'))
    expect(getByTestId('list-page')).toBeTruthy()
    expect(getByTestId('location').textContent).toBe('/agents')
  })

  test('current agent id is marked aria-current="page"', () => {
    const { container } = setup()
    const current = container.querySelector('[aria-current="page"]')
    expect(current?.textContent).toBe('coder')
    expect(current?.className).toContain('font-mono')
  })

  test('title is the display name, falling back to the id', () => {
    const { getByText } = setup({ name: 'Coder' })
    expect(getByText('Coder').tagName).toBe('H1')
    cleanup()
    const bare = setup()
    expect(bare.getByText('coder', { selector: 'h1' })).toBeTruthy()
  })

  test('meta line: model · temp · thinking · workspace (§4.2.2)', () => {
    const { meta } = setup({
      model: { primary: 'anthropic/claude-sonnet-4' },
      temperature: 0.7,
      thinking_level: 'medium',
      workspace: '~/.lele/workspace-coder',
    })
    const text = meta()?.textContent ?? ''
    expect(text).toContain('claude-sonnet-4')
    expect(text).toContain('temp 0.7')
    expect(text).toContain('thinking medium')
    expect(text).toContain('~/.lele/workspace-coder')
    // three separators between four values
    expect(text.split('·').length).toBe(4)
  })

  test('unset fields are omitted from the meta line', () => {
    const { meta } = setup({ model: { primary: 'gpt-4o' } })
    const text = meta()?.textContent ?? ''
    expect(text).toBe('gpt-4o')
  })

  test('an inherited model is shown with the ≈ prefix, never as a choice', () => {
    const { meta } = setup({})
    expect(meta()?.textContent).toBe('≈ gemini-2.5-pro')
  })

  test('no meta line at all when there is nothing to say', () => {
    // No model of its own and no defaults to inherit from → nothing to show.
    const { meta } = setup({}, { defaultsModel: '' })
    expect(meta()).toBeNull()
  })

  test('description renders only when present', () => {
    const { getByText } = setup({ description: 'Escribe código' })
    expect(getByText('Escribe código').tagName).toBe('P')
    cleanup()
    const none = setup()
    expect(none.queryByText('Escribe código')).toBeNull()
  })

  test('modified badge appears only when the agent is dirty (§5.3)', () => {
    const clean = setup()
    expect(clean.queryByTestId('badge-modified')).toBeNull()
    cleanup()
    const dirty = setup({}, { isModified: true })
    expect(dirty.queryByTestId('badge-modified')).toBeTruthy()
  })

  test('default and inherits badges (§3.4.2)', () => {
    const utils = setup({ default: true })
    const { getByTestId } = utils
    expect(getByTestId('badge-inherits')).toBeTruthy()
    expect(utils.getByText('Default')).toBeTruthy()
    // The agent is not dirty: `getByTestId` would throw, so query for it.
    expect(utils.queryByTestId('badge-modified')).toBeNull()
    cleanup()
    // Not the default agent, and with its own model: no badges at all.
    const plain = setup({ model: { primary: 'gpt-4o' } })
    expect(plain.queryByTestId('badge-inherits')).toBeNull()
    expect(plain.queryAllByText('Default')).toHaveLength(0)
  })

  test('"Open chat" is hidden, not disabled, until the backend supports it (§4.2.3)', () => {
    expect(CAN_OPEN_CHAT_WITH_AGENT).toBe(false)
    const { container } = setup()
    expect(container.querySelectorAll('header button')).toHaveLength(1) // only the back arrow
  })

  test('every meta segment carries a title with the full value', () => {
    const { meta } = setup({ model: { primary: 'anthropic/claude-sonnet-4-20250514' } })
    const titles = [...(meta()?.querySelectorAll('[title]') ?? [])].map((node) =>
      node.getAttribute('title'),
    )
    expect(titles).toContain('anthropic/claude-sonnet-4-20250514')
  })
})
