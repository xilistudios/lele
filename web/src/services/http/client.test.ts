import { afterEach, describe, expect, mock, test } from 'bun:test'
import { createApiClient } from './client'
import { ApiError } from './errors'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
})

describe('getAgentCatalog', () => {
  test('requests the per-agent catalog endpoint and parses the response', async () => {
    const fetchMock = mock(
      async (_input: RequestInfo | URL) =>
        new Response(
          JSON.stringify({
            agent_id: 'coder',
            tools: [
              { name: 'exec', description: 'Execute a shell command' },
              { name: 'read_file', description: 'Read a file' },
            ],
            skills: [
              {
                name: 'weather',
                description: 'Get weather',
                source: 'workspace',
                enabled: true,
              },
            ],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
    )

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh_token')
    const catalog = await api.getAgentCatalog('coder')

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      'http://127.0.0.1:18793/api/v1/agents/coder/catalog',
    )
    expect(catalog.agent_id).toBe('coder')
    expect(catalog.tools).toHaveLength(2)
    expect(catalog.tools[0]?.name).toBe('exec')
    expect(catalog.skills[0]?.source).toBe('workspace')
    expect(catalog.skills[0]?.enabled).toBe(true)
  })

  test('URL-encodes the agent id', async () => {
    const fetchMock = mock(
      async (_input: RequestInfo | URL) =>
        new Response(JSON.stringify({ agent_id: 'a/b', tools: [], skills: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    await api.getAgentCatalog('a/b')

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      'http://127.0.0.1:18793/api/v1/agents/a%2Fb/catalog',
    )
  })

  test('throws ApiError with code agent_not_found on 404', async () => {
    globalThis.fetch = mock(
      async (_input: RequestInfo | URL) =>
        new Response(JSON.stringify({ code: 'agent_not_found', message: 'agent not found' }), {
          status: 404,
          headers: { 'Content-Type': 'application/json' },
        }),
    ) as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')

    await expect(api.getAgentCatalog('ghost')).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
      code: 'agent_not_found',
    })

    let caught: unknown
    try {
      await api.getAgentCatalog('ghost')
    } catch (error) {
      caught = error
    }
    expect(caught).toBeInstanceOf(ApiError)
  })
})
