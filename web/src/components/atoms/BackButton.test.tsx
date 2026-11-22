import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import '../../test/i18n'
import { BackButton } from './BackButton'

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

afterEach(cleanup)

describe('BackButton', () => {
  test('pops the in-app history when there is somewhere to go back to', () => {
    const view = render(
      <MemoryRouter initialEntries={['/chat/native:client-1:1', '/skills']}>
        <LocationProbe />
        <Routes>
          <Route path="/chat/:key" element={<div>chat</div>} />
          <Route path="/skills" element={<BackButton />} />
        </Routes>
      </MemoryRouter>,
    )

    fireEvent.click(view.getByTestId('back-button'))
    expect(view.getByTestId('location').textContent).toBe('/chat/native:client-1:1')
  })

  test('falls back to the chat when the page was opened cold', () => {
    const view = render(
      <MemoryRouter initialEntries={['/cron']}>
        <LocationProbe />
        <Routes>
          <Route path="/" element={<div>chat</div>} />
          <Route path="/cron" element={<BackButton />} />
        </Routes>
      </MemoryRouter>,
    )

    fireEvent.click(view.getByTestId('back-button'))
    expect(view.getByTestId('location').textContent).toBe('/')
  })

  test('accepts a custom label and fallback destination', () => {
    const view = render(
      <MemoryRouter initialEntries={['/secrets']}>
        <LocationProbe />
        <Routes>
          <Route path="/settings/agents" element={<div>agents</div>} />
          <Route
            path="/secrets"
            element={<BackButton label="Volver a agentes" fallbackTo="/settings/agents" />}
          />
        </Routes>
      </MemoryRouter>,
    )

    const button = view.getByLabelText('Volver a agentes')
    expect(button.getAttribute('title')).toBe('Volver a agentes')

    fireEvent.click(button)
    expect(view.getByTestId('location').textContent).toBe('/settings/agents')
  })

  test('defaults the label to common.back', () => {
    const view = render(
      <MemoryRouter initialEntries={['/skills']}>
        <BackButton />
      </MemoryRouter>,
    )
    expect(view.getByLabelText('Atrás')).not.toBeNull()
  })
})
