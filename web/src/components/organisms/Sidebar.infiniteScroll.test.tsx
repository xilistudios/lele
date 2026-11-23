import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render, within } from '@testing-library/react'
import { createElement, useState } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { ChatMode } from '../../lib/types'
import { Sidebar } from './Sidebar'

const authValue = { session: { device_name: 'Desktop' } } as unknown as AuthContextValue

type ObserverCallback = (entries: { isIntersecting: boolean }[]) => void

const originalIntersectionObserver = globalThis.IntersectionObserver

function installFakeObserver() {
  const callbacks: ObserverCallback[] = []

  class FakeIntersectionObserver {
    constructor(callback: ObserverCallback) {
      callbacks.push(callback)
    }
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return []
    }
    static latest() {
      return callbacks[callbacks.length - 1]
    }
    static count() {
      return callbacks.length
    }
  }

  globalThis.IntersectionObserver =
    FakeIntersectionObserver as unknown as typeof IntersectionObserver
  return FakeIntersectionObserver
}

const sessions = (count: number, mode: ChatMode = 'agent') =>
  Array.from({ length: count }, (_, i) => ({
    key: `native:session-${i}`,
    name: `Session ${i}`,
    kind: 'chat',
    mode,
    updated: new Date(Date.now() - i * 60_000).toISOString(),
  }))

function logicValue(list: unknown[], chatMode: ChatMode): AppLogicContextValue {
  return {
    sessions: list,
    currentSessionKey: 'native:session-0',
    parentSessionKey: null,
    processingSessions: new Set<string>(),
    chatMode,
    groupsEnabled: false,
    onCreateSession: () => undefined,
    onDeleteSession: () => undefined,
    onToggleSidebar: () => undefined,
    onSelectMode: () => undefined,
    onSelectSession: () => undefined,
    onLogout: async () => undefined,
  } as unknown as AppLogicContextValue
}

function renderSidebar(list: unknown[], chatMode: ChatMode = 'agent') {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue(list, chatMode) },
        createElement(
          MemoryRouter,
          { initialEntries: ['/'] },
          createElement(Sidebar, { collapsed: false, mobileOpen: false, onClose: () => undefined }),
        ),
      ),
    ),
  )
}

const sessionControls = (view: ReturnType<typeof render>) =>
  within(view.container).queryAllByRole('button', { name: /^Session \d+$/ })

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  globalThis.IntersectionObserver = originalIntersectionObserver
})

describe('Sidebar — chat history pagination', () => {
  test('renders only the first page of sessions', () => {
    installFakeObserver()
    const view = renderSidebar(sessions(40))

    expect(sessionControls(view).length).toBe(15)
  })

  test('reveals one more page when the sentinel becomes visible', () => {
    const observer = installFakeObserver()
    const view = renderSidebar(sessions(40))

    act(() => observer.latest()([{ isIntersecting: true }]))
    expect(sessionControls(view).length).toBe(30)
  })

  test('stops at the last session and hides the sentinel', () => {
    const observer = installFakeObserver()
    const view = renderSidebar(sessions(20))

    act(() => observer.latest()([{ isIntersecting: true }]))
    expect(sessionControls(view).length).toBe(20)
    expect(view.container.querySelector('[data-testid="sidebar-history-sentinel"]')).toBeNull()
  })

  test('a short history needs no pagination', () => {
    installFakeObserver()
    const view = renderSidebar(sessions(3))

    expect(sessionControls(view).length).toBe(3)
    expect(observerCount()).toBe(0)
  })

  test('switching chat mode starts again from the first page', () => {
    const observer = installFakeObserver()

    function Harness() {
      const [mode, setMode] = useState<ChatMode>('agent')
      return createElement(
        'div',
        null,
        createElement(
          AppLogicContext.Provider,
          {
            value: logicValue(
              [
                ...sessions(40, 'agent'),
                ...sessions(40, 'group').map((s) => ({ ...s, key: `p-${s.key}` })),
              ],
              mode,
            ),
          },
          createElement(
            MemoryRouter,
            { initialEntries: ['/'] },
            createElement(Sidebar, {
              collapsed: false,
              mobileOpen: false,
              onClose: () => undefined,
            }),
            createElement(
              'button',
              { type: 'button', onClick: () => setMode('group') },
              'switch mode',
            ),
          ),
        ),
      )
    }

    const view = render(
      createElement(AuthContext.Provider, { value: authValue }, createElement(Harness)),
    )

    act(() => observer.latest()([{ isIntersecting: true }]))
    expect(sessionControls(view).length).toBe(30)

    act(() => fireEvent.click(view.getByRole('button', { name: 'switch mode' })))
    expect(sessionControls(view).length).toBe(15)
  })
})

function observerCount() {
  return (globalThis.IntersectionObserver as unknown as { count: () => number }).count()
}
