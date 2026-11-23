import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import type { SubagentTaskInfo } from '../../lib/types'
import { SubagentsSidebar } from './SubagentsSidebar'

function subagent(overrides: Partial<SubagentTaskInfo> = {}): SubagentTaskInfo {
  return {
    task_id: 'subagent-1',
    session_key: 'session-1:subagent-1',
    label: 'worker',
    agent_id: 'main',
    status: 'completed',
    summary: '',
    created: Date.now(),
    updated: Date.now(),
    iterations: 0,
    ...overrides,
  }
}

function renderSidebar(subagents: SubagentTaskInfo[]) {
  return render(
    createElement(SubagentsSidebar, {
      subagents,
      loading: false,
      isOpen: true,
      onClose: () => {},
      onSelectSubagent: () => {},
    }),
  )
}

const DISTINGUISHABLE_STATUSES: Array<SubagentTaskInfo['status']> = [
  'running',
  'pending',
  'completed',
  'failed',
  'cancelled',
  'needs_context',
  'not_done',
]

afterEach(cleanup)

describe('SubagentsSidebar status badges', () => {
  test('renders the raw status text in the badge', () => {
    const view = renderSidebar([subagent({ status: 'failed' })])
    expect(view.getByText('failed')).not.toBeNull()
  })

  test('goes full-screen on mobile', () => {
    const view = renderSidebar([subagent()])

    const classes = (view.getByRole('dialog').getAttribute('class') ?? '').split(/\s+/)
    for (const token of ['max-md:w-full', 'max-md:max-w-full']) {
      expect(classes).toContain(token)
    }
  })

  test('uses a distinct color class per status family', () => {
    const view = renderSidebar(
      DISTINGUISHABLE_STATUSES.map((status, i) =>
        subagent({ task_id: `s-${i}`, status, label: `worker-${status}` }),
      ),
    )
    const colorByStatus = new Map<string, string>()
    for (const status of DISTINGUISHABLE_STATUSES) {
      const badge = view.getByText(status)
      const cls = badge.className
      colorByStatus.set(status, cls)
      expect(cls).toContain('rounded')
    }

    expect(colorByStatus.get('running')).not.toBe(colorByStatus.get('completed'))
    expect(colorByStatus.get('failed')).not.toBe(colorByStatus.get('completed'))
    expect(colorByStatus.get('pending')).toBe(colorByStatus.get('running'))
    expect(colorByStatus.get('needs_context')).not.toBe(colorByStatus.get('running'))
  })

  test('shows the spinner for running and pending subagents', () => {
    renderSidebar([
      subagent({ task_id: 's-run', status: 'running', label: 'runner' }),
      subagent({ task_id: 's-pend', status: 'pending', label: 'waiter' }),
      subagent({ task_id: 's-done', status: 'completed', label: 'done-one' }),
    ])
    const spinners = document.querySelectorAll('.animate-spin')
    expect(spinners.length).toBe(2)
  })
})
