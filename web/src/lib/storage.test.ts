import { describe, expect, test } from 'bun:test'
import {
  clearCurrentSessionKey,
  clearSession,
  loadCurrentSessionKey,
  loadSession,
  saveCurrentSessionKey,
  saveSession,
} from './storage'

describe('storage', () => {
  test('saves and loads a session', () => {
    clearSession()
    saveSession({ token: 'a', refresh_token: 'b', expires: 'c', client_id: 'd' })

    expect(loadSession()?.token).toBe('a')
  })

  test('saves and clears current session key', () => {
    clearCurrentSessionKey()
    saveCurrentSessionKey('native:client')

    expect(loadCurrentSessionKey()).toBe('native:client')

    clearCurrentSessionKey()
    expect(loadCurrentSessionKey()).toBeNull()
  })
})
