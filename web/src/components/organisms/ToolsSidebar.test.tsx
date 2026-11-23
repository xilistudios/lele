import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render, waitFor, within } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import i18n from '../../test/i18n'
import { ToolsSidebar } from './ToolsSidebar'

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

const drawerName = i18n.t('chat.tools')

const statusLabel = (status: 'connected' | 'connecting' | 'disconnected') =>
  i18n.t('connection.statusAria', { status })

function renderDrawer(
  isOpen = true,
  connectionStatus: 'connected' | 'connecting' | 'disconnected' = 'connected',
) {
  const view = render(
    <MemoryRouter initialEntries={['/']}>
      <ToolsSidebar
        isOpen={isOpen}
        onClose={() => undefined}
        connectionStatus={connectionStatus}
        apiUrl="http://127.0.0.1:18793"
      />
      <LocationProbe />
    </MemoryRouter>,
  )

  return { ...view, drawer: () => view.queryByRole('dialog', { name: drawerName }) }
}

afterEach(cleanup)

describe('ToolsSidebar', () => {
  test('renders nothing while closed', () => {
    const view = renderDrawer(false)
    expect(view.drawer()).toBeNull()
  })

  test('opens with the four tooling destinations', async () => {
    const view = renderDrawer()

    await waitFor(() => expect(view.drawer()).not.toBeNull())

    for (const label of [
      i18n.t('sidebar.skills'),
      i18n.t('sidebar.cron'),
      i18n.t('sidebar.backgroundExecs'),
      i18n.t('sidebar.secrets'),
    ]) {
      expect(view.getByRole('button', { name: label })).not.toBeNull()
    }
  })

  test('each row navigates to its page', async () => {
    const view = renderDrawer()
    await waitFor(() => expect(view.drawer()).not.toBeNull())

    fireEvent.click(view.getByRole('button', { name: i18n.t('sidebar.cron') }))
    expect(view.getByTestId('location').textContent).toBe('/cron')

    fireEvent.click(view.getByRole('button', { name: i18n.t('sidebar.secrets') }))
    expect(view.getByTestId('location').textContent).toBe('/secrets')
  })

  test('the connection status lives in the drawer footer', async () => {
    const view = renderDrawer()
    await waitFor(() => expect(view.drawer()).not.toBeNull())

    expect(
      view.drawer()?.contains(view.getByRole('button', { name: statusLabel('connected') })),
    ).toBe(true)
  })

  test('the footer status is a bare dot, not an icon', async () => {
    const view = renderDrawer()
    await waitFor(() => expect(view.drawer()).not.toBeNull())

    const trigger = view.getByRole('button', { name: statusLabel('connected') })
    expect(trigger.querySelector('svg')).toBeNull()
    expect(trigger.children.length).toBe(1)
    expect(trigger.firstElementChild?.className).toContain('rounded-full')
  })

  test('the dot color follows the connection status', async () => {
    const colors = new Map<string, string>()
    for (const status of ['connected', 'connecting', 'disconnected'] as const) {
      const view = renderDrawer(true, status)
      await waitFor(() => expect(view.drawer()).not.toBeNull())
      colors.set(
        status,
        view.getByRole('button', { name: statusLabel(status) }).firstElementChild?.className ?? '',
      )
      cleanup()
    }

    expect(new Set(colors.values()).size).toBe(3)
  })

  test('Escape closes the drawer', async () => {
    let closed = 0
    const view = render(
      <MemoryRouter initialEntries={['/']}>
        <ToolsSidebar
          isOpen
          onClose={() => {
            closed += 1
          }}
          connectionStatus="connected"
        />
      </MemoryRouter>,
    )
    await waitFor(() => expect(view.queryByRole('dialog', { name: drawerName })).not.toBeNull())

    act(() => {
      fireEvent.keyDown(document, { key: 'Escape' })
    })
    expect(closed).toBe(1)
  })

  test('the close button closes the drawer', async () => {
    let closed = 0
    const view = render(
      <MemoryRouter initialEntries={['/']}>
        <ToolsSidebar
          isOpen
          onClose={() => {
            closed += 1
          }}
          connectionStatus="connected"
        />
      </MemoryRouter>,
    )
    const drawer = await waitFor(() => {
      const found = view.queryByRole('dialog', { name: drawerName })
      if (!found) throw new Error('drawer not open')
      return found
    })

    fireEvent.click(within(drawer).getByRole('button', { name: i18n.t('common.close') }))
    expect(closed).toBe(1)
  })
})
