import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import '../test/i18n'
import { AuthView } from './AuthView'

describe('AuthView', () => {
  test('keeps submit disabled until inputs are valid', () => {
    render(
      <AuthView apiUrl="http://localhost" error={null} onSubmit={mock(async () => undefined)} />,
    )

    const submit = screen.getByRole('button', { name: 'Conectar' })
    expect((submit as HTMLButtonElement).disabled).toBe(true)

    fireEvent.change(screen.getByPlaceholderText('123456'), { target: { value: '123456' } })
    expect((submit as HTMLButtonElement).disabled).toBe(false)
  })

  test('submits pin and device name', async () => {
    const onSubmit = mock(async () => undefined)
    render(<AuthView apiUrl="http://localhost" error={null} onSubmit={onSubmit} />)

    fireEvent.change(screen.getByPlaceholderText('123456'), { target: { value: '123456' } })
    fireEvent.change(screen.getByPlaceholderText('My Desktop'), { target: { value: 'Office PC' } })
    fireEvent.click(screen.getByRole('button', { name: 'Conectar' }))

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        apiUrl: 'http://localhost',
        pin: '123456',
        deviceName: 'Office PC',
      })
    })
  })
})
