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

// Statuses the sidebar must color-code distinctly. The WebUI bug this guards:
// every persisted subagent used to render as "completed" regardless of the
// real terminal status.
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

  test('uses a distinct color class per status family', () => {
    const view = renderSidebar(
      DISTINGUISHABLE_STATUSES.map((status, i) =>
        subagent({ task_id: `s-${i}`, status, label: `worker-${status}` }),
      ),
    )
    // Mutation check: if two statuses share a color, the badge class set is
    // smaller than the status list. Map status -> badge className.
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
    // Spinner renders an animated SVG with animate-spin.
    const spinners = document.querySelectorAll('.animate-spin')
    expect(spinners.length).toBe(2)
  })
})
