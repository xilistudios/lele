import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import '../../test/i18n'
import { ToolsSidebar } from './ToolsSidebar'

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

function renderDrawer(isOpen = true) {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <ToolsSidebar
        isOpen={isOpen}
        onClose={() => undefined}
        connectionStatus="connected"
        apiUrl="http://127.0.0.1:18793"
      />
      <LocationProbe />
    </MemoryRouter>,
  )
}

afterEach(cleanup)

describe('ToolsSidebar', () => {
  test('renders nothing while closed', () => {
    const view = renderDrawer(false)
    expect(view.queryByTestId('tools-sidebar')).toBeNull()
  })

  test('opens with the four tooling destinations', async () => {
    const view = renderDrawer()

    await waitFor(() => expect(view.getByTestId('tools-sidebar')).not.toBeNull())

    const panel = view.getByTestId('tools-sidebar')
    for (const label of [
      'Habilidades',
      'Tareas programadas',
      'Procesos en Segundo Plano',
      'Secretos',
    ]) {
      expect(panel.textContent).toContain(label)
    }
  })

  test('each row navigates to its page', async () => {
    const view = renderDrawer()
    await waitFor(() => expect(view.getByTestId('tools-sidebar')).not.toBeNull())

    fireEvent.click(view.getByLabelText('Tareas programadas'))
    expect(view.getByTestId('location').textContent).toBe('/cron')

    fireEvent.click(view.getByLabelText('Secretos'))
    expect(view.getByTestId('location').textContent).toBe('/secrets')
  })

  test('the connection status lives in the drawer footer', async () => {
    const view = renderDrawer()
    await waitFor(() => expect(view.getByTestId('tools-sidebar')).not.toBeNull())

    const panel = view.getByTestId('tools-sidebar')
    expect(panel.querySelector('button[aria-label^="Estado de conexión"]')).not.toBeNull()
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
    await waitFor(() => expect(view.getByTestId('tools-sidebar')).not.toBeNull())

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
    await waitFor(() => expect(view.getByTestId('tools-sidebar')).not.toBeNull())

    const closeBtn = view
      .getByTestId('tools-sidebar')
      .querySelector('button[aria-label="Cerrar"]') as HTMLButtonElement
    fireEvent.click(closeBtn)
    expect(closed).toBe(1)
  })
})
