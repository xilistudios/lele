import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import type { GroupInfo } from '../../lib/types'
import i18n from '../../test/i18n'
import { GroupChatPanel } from './GroupChatPanel'

const group: GroupInfo = {
  groupID: 'g-1',
  status: 'done',
  strategy: 'debate',
  participants: 'main,ui-engineer',
  layers: 1,
  totalTokens: 120,
  createdAt: '2026-01-01T00:00:00Z',
  turns: [
    {
      groupID: 'g-1',
      speaker: 'main',
      label: 'Main Agent',
      role: 'proposer',
      layer: 0,
      turnIndex: 0,
      content: 'The proposal body',
    },
  ],
}

function renderPanel(groups: GroupInfo[] = [group]) {
  let closed = 0
  const view = render(
    <GroupChatPanel
      groups={groups}
      isOpen
      onClose={() => {
        closed += 1
      }}
    />,
  )
  return { ...view, closed: () => closed }
}

afterEach(cleanup)

describe('GroupChatPanel', () => {
  test('exposes the panel as a named modal dialog', () => {
    const view = renderPanel()
    const drawer = view.getByRole('dialog', { name: i18n.t('groups.title') })

    expect(drawer.getAttribute('aria-modal')).toBe('true')
  })

  test('the close action is reachable by its accessible name', () => {
    const view = renderPanel()
    const close = view.getByRole('button', { name: i18n.t('common.close') })

    fireEvent.click(close)
    expect(view.closed()).toBe(1)
  })

  test('renders the turns of the running group', () => {
    const view = renderPanel()

    expect(view.getByText('Main Agent')).not.toBeNull()
    expect(view.getByText('The proposal body')).not.toBeNull()
  })

  test('explains the empty state when no group is active', () => {
    const view = renderPanel([])

    expect(view.getByText(i18n.t('groups.noGroups'))).not.toBeNull()
  })
})
