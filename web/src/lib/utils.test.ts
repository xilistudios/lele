import { describe, expect, test } from 'bun:test'
import { formatSessionTitle } from './utils'

describe('formatSessionTitle', () => {
  test('usa el nombre de sesión si existe', () => {
    const title = formatSessionTitle('native:client:1', 'Mi Chat', 5)
    expect(title).toBe('Mi Chat')
  })

  test('usa "New Chat" si no hay nombre ni mensajes', () => {
    const title = formatSessionTitle('native:client:1')
    expect(title).toBe('New Chat')
  })

  test('usa "New Chat" si message_count es 0', () => {
    const title = formatSessionTitle('native:client:1', '', 0)
    expect(title).toBe('New Chat')
  })

  test('extrae "Session N" del último segmento del sessionKey', () => {
    const title = formatSessionTitle('native:client:42', '', 5)
    expect(title).toBe('Session 42')
  })

  test('usa el sessionKey completo si no tiene suficientes partes', () => {
    const title = formatSessionTitle('just-a-key', '', 3)
    expect(title).toBe('just-a-key')
  })

  test('trimea espacios en blanco en sessionName', () => {
    const title = formatSessionTitle('native:client:1', '  Hola', 5)
    expect(title).toBe('  Hola')
  })
})
