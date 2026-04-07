import { afterEach, describe, expect, mock, test } from 'bun:test'
import { createApiClient } from './api'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
})

describe('createApiClient', () => {
  test('builds requests with auth headers', async () => {
    const fetchMock = mock(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(
          JSON.stringify({
            valid: true,
            client_id: 'client',
            device_name: 'device',
            expires: '2026-01-01T00:00:00Z',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
    )

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    const response = await api.status('token')

    expect(response.valid).toBe(true)
    expect(fetchMock).toHaveBeenCalled()
  })

  test('loads sessions and channels', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      const body = url.endsWith('/api/v1/chat/sessions')
        ? {
            sessions: [
              {
                key: 'native:client',
                created: '2026-01-01T00:00:00Z',
                updated: '2026-01-01T00:00:00Z',
                message_count: 2,
              },
            ],
          }
        : url.endsWith('/api/v1/channels')
          ? { channels: [{ name: 'native', enabled: true, running: true }] }
          : {}

      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh_token')
    const sessions = await api.sessions()
    const channels = await api.channels()

    expect(sessions.sessions[0]?.key).toBe('native:client')
    expect(channels.channels[0]?.name).toBe('native')
  })

  test('loads and updates session model', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const body =
        init?.method === 'PATCH'
          ? {
              session_key: 'native:client',
              model: 'gpt-4o-mini',
              models: ['gpt-4', 'gpt-4o-mini'],
              model_groups: [
                {
                  provider: 'openai',
                  models: [
                    { value: 'gpt-4', label: 'gpt-4' },
                    { value: 'gpt-4o-mini', label: 'gpt-4o-mini' },
                  ],
                },
              ],
            }
          : url.includes('/api/v1/chat/session/')
            ? {
                session_key: 'native:client',
                model: 'gpt-4',
                models: ['gpt-4', 'gpt-4o-mini'],
                model_groups: [
                  {
                    provider: 'openai',
                    models: [
                      { value: 'gpt-4', label: 'gpt-4' },
                      { value: 'gpt-4o-mini', label: 'gpt-4o-mini' },
                    ],
                  },
                ],
              }
            : {
                agent_id: 'main',
                model: 'gpt-4',
                models: ['gpt-4', 'gpt-4o-mini'],
                model_groups: [
                  {
                    provider: 'openai',
                    models: [
                      { value: 'gpt-4', label: 'gpt-4' },
                      { value: 'gpt-4o-mini', label: 'gpt-4o-mini' },
                    ],
                  },
                ],
              }

      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh_token')
    const available = await api.models('main', 'native:client')
    const updated = await api.updateSessionModel('native:client', 'gpt-4o-mini')

    expect(available.models).toContain('gpt-4')
    expect(available.model_groups?.[0]?.provider).toBe('openai')
    expect(updated.model).toBe('gpt-4o-mini')
  })

  test('updates rotated refresh token after automatic refresh', async () => {
    let refreshCalls = 0
    const fetchMock = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)

      if (url.endsWith('/api/v1/auth/refresh')) {
        refreshCalls += 1
        const body = JSON.parse(String(init?.body ?? '{}')) as { refresh_token?: string }

        if (refreshCalls === 1) {
          expect(body.refresh_token).toBe('refresh-1')
          return new Response(
            JSON.stringify({
              token: 'token-2',
              refresh_token: 'refresh-2',
              expires: '2026-01-01T00:00:00Z',
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          )
        }

        expect(body.refresh_token).toBe('refresh-2')
        return new Response(
          JSON.stringify({
            token: 'token-3',
            refresh_token: 'refresh-3',
            expires: '2026-01-01T00:00:00Z',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }

      return new Response(JSON.stringify({ error: 'unauthorized' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token-1', 'refresh-1')

    await expect(api.sessions()).rejects.toThrow()
    await expect(api.channels()).rejects.toThrow()

    expect(refreshCalls).toBe(2)
  })
})
