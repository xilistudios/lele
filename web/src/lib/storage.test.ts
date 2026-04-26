import { afterEach, describe, expect, test } from 'bun:test'
import {
  clearCurrentSessionKey,
  clearSession,
  loadApiUrl,
  loadCurrentSessionKey,
  loadSession,
  saveApiUrl,
  saveCurrentSessionKey,
  saveSession,
} from './storage'

describe('storage', () => {
  afterEach(() => {
    localStorage.clear()
  })

  test('saves and loads a session', () => {
    clearSession()
    saveSession({ token: 'a', refresh_token: 'b', expires: 'c', client_id: 'd' })

    const session = loadSession()
    expect(session?.token).toBe('a')
    expect(session?.refresh_token).toBe('b')
    expect(session?.expires).toBe('c')
    expect(session?.client_id).toBe('d')
  })

  test('returns null when no session saved', () => {
    localStorage.clear()
    expect(loadSession()).toBeNull()
  })

  test('returns null when session data is corrupted', () => {
    localStorage.setItem('lele.session', 'not-json')
    expect(loadSession()).toBeNull()
  })

  test('clearSession removes the session', () => {
    saveSession({ token: 'a', refresh_token: 'b', expires: 'c', client_id: 'd' })
    clearSession()
    expect(loadSession()).toBeNull()
  })

  test('saves and clears current session key', () => {
    clearCurrentSessionKey()
    saveCurrentSessionKey('native:client')

    expect(loadCurrentSessionKey()).toBe('native:client')

    clearCurrentSessionKey()
    expect(loadCurrentSessionKey()).toBeNull()
  })

  test('ignores subagent session keys (lele.currentSessionKey)', () => {
    clearCurrentSessionKey()
    saveCurrentSessionKey('subagent:task-1')
    // Debería ignorar la subagent key
    expect(loadCurrentSessionKey()).toBeNull()
  })

  test('loadApiUrl returns fallback when no URL saved', () => {
    localStorage.removeItem('lele.apiUrl')
    expect(loadApiUrl('http://localhost:18793')).toBe('http://localhost:18793')
  })

  test('loadApiUrl returns saved URL', () => {
    saveApiUrl('http://custom:9999')
    expect(loadApiUrl('http://localhost:18793')).toBe('http://custom:9999')
  })

  test('saveApiUrl persists the URL', () => {
    saveApiUrl('http://new:8080')
    expect(localStorage.getItem('lele.apiUrl')).toBe('http://new:8080')
  })

  test('saveSession does not persist subagent session keys', () => {
    clearCurrentSessionKey()
    saveCurrentSessionKey('native:client:1')
    expect(loadCurrentSessionKey()).toBe('native:client:1')
  })
})
