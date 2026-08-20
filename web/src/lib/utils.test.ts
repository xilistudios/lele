import { describe, expect, test } from 'bun:test'
import { formatSessionTitle } from './utils'

describe('formatSessionTitle', () => {
  test('usa el nombre de sesión si existe', () => {
    const title = formatSessionTitle('native:client:1', 'Mi Chat')
    expect(title).toBe('Mi Chat')
  })

  test('usa "New Chat" si no hay nombre y el sessionKey parece un UUID', () => {
    const title = formatSessionTitle('a1b2c3d4-e5f6-7890-abcd-ef1234567890')
    expect(title).toBe('New Chat')
  })

  test('deriva "Session N" del sessionKey cuando no es UUID', () => {
    const title = formatSessionTitle('native:client:42')
    expect(title).toBe('Session 42')
  })

  test('usa el sessionKey completo si no tiene suficientes partes', () => {
    const title = formatSessionTitle('just-a-key')
    expect(title).toBe('just-a-key')
  })

  test('trimea espacios en blanco en sessionName', () => {
    const title = formatSessionTitle('native:client:1', '  Hola')
    expect(title).toBe('  Hola')
  })
})