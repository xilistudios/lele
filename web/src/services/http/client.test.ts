import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { loadSession, saveSession } from '../../lib/storage'
import type { AuthSession } from '../../lib/types'
import { createApiClient, isFatalRefreshFailure } from './client'
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

describe('per-agent skill mutations', () => {
  function captureFetch(payload: unknown = { message: 'ok' }) {
    const seen: { url: string; method?: string; body?: BodyInit | null }[] = []
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({ url: String(input), method: init?.method, body: init?.body })
      return new Response(JSON.stringify(payload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as unknown as typeof fetch
    return seen
  }

  const api = () => createApiClient('http://127.0.0.1:18793')

  test('agentToggleSkill PUTs {enabled} to the agent skill route', async () => {
    const seen = captureFetch()
    await api().agentToggleSkill('coder', 'weather', false)

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/skills/weather/toggle')
    expect(seen[0]?.method).toBe('PUT')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({ enabled: false })
  })

  test('agentToggleSkill encodes both the agent id and the skill name', async () => {
    const seen = captureFetch()
    await api().agentToggleSkill('a b', 'c/d', true)

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/a%20b/skills/c%2Fd/toggle')
  })

  test('agentRemoveSkill DELETEs the skill route', async () => {
    const seen = captureFetch()
    await api().agentRemoveSkill('coder', 'chrome')

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/skills/chrome')
    expect(seen[0]?.method).toBe('DELETE')
  })

  test('agentInstallSkill POSTs url + scope', async () => {
    const seen = captureFetch({ skill_id: 'tmux', message: 'ok' })
    const response = await api().agentInstallSkill('coder', 'sipeed/lele-skills/tmux', 'global')

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/skills/install')
    expect(seen[0]?.method).toBe('POST')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({
      url: 'sipeed/lele-skills/tmux',
      scope: 'global',
    })
    expect(response.skill_id).toBe('tmux')
  })

  test('agentInstallSkill without a scope sends scope undefined (server default)', async () => {
    const seen = captureFetch()
    await api().agentInstallSkill('coder', 'owner/repo/skill')

    expect(JSON.parse(String(seen[0]?.body))).toEqual({ url: 'owner/repo/skill' })
  })

  test('agentInstallSkillsBatch POSTs repo + skills + scope', async () => {
    const seen = captureFetch({ installed: ['a', 'b'], count: 2, message: 'ok' })
    const response = await api().agentInstallSkillsBatch(
      'coder',
      'owner/repo',
      ['a', 'b'],
      'workspace',
    )

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/skills/install-batch')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({
      repo: 'owner/repo',
      skills: ['a', 'b'],
      scope: 'workspace',
    })
    expect(response.count).toBe(2)
  })

  test('a rejected mutation surfaces the server error code', async () => {
    globalThis.fetch = mock(
      async (_input: RequestInfo | URL) =>
        new Response(
          JSON.stringify({ code: 'skill_not_deletable', message: 'global skill is shared' }),
          { status: 400, headers: { 'Content-Type': 'application/json' } },
        ),
    ) as unknown as typeof fetch

    await expect(api().agentRemoveSkill('coder', 'weather')).rejects.toMatchObject({
      name: 'ApiError',
      status: 400,
      code: 'skill_not_deletable',
      message: 'global skill is shared',
    })
  })

  test('installSkill POSTs url + scope', async () => {
    const seen = captureFetch({ skill_id: 'tmux', message: 'ok' })
    const response = await api().installSkill('sipeed/lele-skills/tmux', 'global')

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/skills')
    expect(seen[0]?.method).toBe('POST')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({
      url: 'sipeed/lele-skills/tmux',
      scope: 'global',
    })
    expect(response.skill_id).toBe('tmux')
  })

  test('installSkill without a scope omits the key exactly', async () => {
    const seen = captureFetch()
    await api().installSkill('owner/repo/skill')

    expect(JSON.parse(String(seen[0]?.body))).toEqual({ url: 'owner/repo/skill' })
  })

  test('installSkillsBatch POSTs repo + skills + scope', async () => {
    const seen = captureFetch({ installed: ['a'], count: 1, message: 'ok' })
    const response = await api().installSkillsBatch('owner/repo', ['a', 'b'], 'global')

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/skills/install-batch')
    expect(seen[0]?.method).toBe('POST')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({
      repo: 'owner/repo',
      skills: ['a', 'b'],
      scope: 'global',
    })
    expect(response.count).toBe(1)
  })
})

describe('per-agent command endpoints', () => {
  function captureFetch(payload: unknown = { ok: true }) {
    const seen: { url: string; method?: string; body?: BodyInit | null }[] = []
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({ url: String(input), method: init?.method, body: init?.body })
      return new Response(JSON.stringify(payload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as unknown as typeof fetch
    return seen
  }

  const api = () => createApiClient('http://127.0.0.1:18793')

  test('agentCommands GETs the list route', async () => {
    const seen = captureFetch({ commands: [], builtin: [] })
    await api().agentCommands('coder')
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/commands')
    expect(seen[0]?.method).toBe('GET')
  })

  test('agentCommand GETs one command and encodes the name', async () => {
    const seen = captureFetch({
      name: 'a b',
      content: '',
      path: '',
      source: 'workspace',
      deletable: true,
    })
    await api().agentCommand('coder', 'a b')
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/commands/a%20b')
    expect(seen[0]?.method).toBe('GET')
  })

  test('agentCommandCreate POSTs {name, content, scope} to the list route', async () => {
    const seen = captureFetch({ ok: true, command: { name: 'deploy' } })
    const response = await api().agentCommandCreate('coder', {
      name: 'deploy',
      content: '---\ndescription: Deploy\n---\nbody',
      scope: 'global',
    })
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/commands')
    expect(seen[0]?.method).toBe('POST')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({
      name: 'deploy',
      content: '---\ndescription: Deploy\n---\nbody',
      scope: 'global',
    })
    expect(response.command?.name).toBe('deploy')
  })

  test('agentCommandUpdate PUTs {content} to the command route', async () => {
    const seen = captureFetch({ ok: true })
    await api().agentCommandUpdate('a/b', 'deploy', { content: 'new markdown' })
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/a%2Fb/commands/deploy')
    expect(seen[0]?.method).toBe('PUT')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({ content: 'new markdown' })
  })

  test('agentCommandRemove DELETEs the command route', async () => {
    const seen = captureFetch({ ok: true })
    await api().agentCommandRemove('coder', 'deploy')
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/agents/coder/commands/deploy')
    expect(seen[0]?.method).toBe('DELETE')
  })

  test('a rejected write surfaces the server error code', async () => {
    globalThis.fetch = mock(
      async () =>
        new Response(
          JSON.stringify({ error: 'config-level commands are not editable', code: 'not_editable' }),
          { status: 403, headers: { 'Content-Type': 'application/json' } },
        ),
    ) as unknown as typeof fetch

    await expect(
      api().agentCommandUpdate('coder', 'deploy', { content: 'x' }),
    ).rejects.toMatchObject({ name: 'ApiError', status: 403, code: 'not_editable' })
  })
})

describe('per-agent MCP endpoints', () => {
  function captureFetch(payload: unknown = { ok: true }) {
    const seen: { url: string; method?: string; body?: BodyInit | null }[] = []
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({ url: String(input), method: init?.method, body: init?.body })
      return new Response(JSON.stringify(payload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as unknown as typeof fetch
    return seen
  }

  const api = () => createApiClient('http://127.0.0.1:18793')

  test('mcpInventory GETs the merged inventory', async () => {
    const seen = captureFetch({ agent_id: 'main' })
    await api().mcpInventory('main')
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp?agent_id=main')
    expect(seen[0]?.method).toBe('GET')
  })

  test('mcpRaw GETs the per-layer raw file', async () => {
    const seen = captureFetch({ layer: 'agent', exists: false, content: '' })
    await api().mcpRaw('main', 'agent')
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp/agent/raw?agent_id=main')
    expect(seen[0]?.method).toBe('GET')
  })

  test('mcpPutRaw PUTs {content} to the raw route', async () => {
    const seen = captureFetch({ layer: 'agent', exists: true, content: '{}' })
    await api().mcpPutRaw('main', 'agent', { content: '{"mcpServers":{}}' })
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp/agent/raw?agent_id=main')
    expect(seen[0]?.method).toBe('PUT')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({ content: '{"mcpServers":{}}' })
  })

  test('mcpToggle PUTs {enabled} to the toggle route', async () => {
    const seen = captureFetch({ name: 'srv/ice', enabled: false })
    await api().mcpToggle('main', 'agent', 'srv/ice', false)
    expect(seen[0]?.url).toBe(
      'http://127.0.0.1:18793/api/v1/mcp/agent/servers/srv%2Fice/toggle?agent_id=main',
    )
    expect(seen[0]?.method).toBe('PUT')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({ enabled: false })
  })

  test('mcpToggle with force appends force=true', async () => {
    const seen = captureFetch({ name: 'srv', enabled: false })
    await api().mcpToggle('a b', 'auto', 'srv', false, true)
    expect(seen[0]?.url).toBe(
      'http://127.0.0.1:18793/api/v1/mcp/auto/servers/srv/toggle?agent_id=a%20b&force=true',
    )
  })

  test('mcpValidate POSTs {layer, content}', async () => {
    const seen = captureFetch({ valid: true })
    await api().mcpValidate('main', { layer: 'project', content: '{}' })
    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp/validate?agent_id=main')
    expect(seen[0]?.method).toBe('POST')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({ layer: 'project', content: '{}' })
  })

  function captureFetch204() {
    const seen: { url: string; method?: string; body?: BodyInit | null }[] = []
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({ url: String(input), method: init?.method, body: init?.body })
      return new Response(null, { status: 204 })
    }) as unknown as typeof fetch
    return seen
  }

  test('mcpAuthStart POSTs with NO body to the server auth route', async () => {
    const seen = captureFetch({
      flow_id: 'abc',
      auth_url: 'https://idp.example/authorize?x=1',
      redirect_uri: 'http://127.0.0.1:18794/callback',
      expires_at: '2026-01-01T00:05:00Z',
    })
    const start = await api().mcpAuthStart('a b', 'srv/ice')

    expect(seen[0]?.url).toBe(
      'http://127.0.0.1:18793/api/v1/mcp/servers/srv%2Fice/auth?agent_id=a%20b',
    )
    expect(seen[0]?.method).toBe('POST')
    expect(seen[0]?.body ?? null).toBeNull()
    expect(start.flow_id).toBe('abc')
    expect(start.auth_url).toBe('https://idp.example/authorize?x=1')
  })

  test('mcpAuthStatus GETs the flow route', async () => {
    const seen = captureFetch({
      flow_id: 'f/1',
      server: 'srv',
      state: 'pending',
      authenticated: false,
    })
    const status = await api().mcpAuthStatus('main', 'f/1')

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp/auth/f%2F1?agent_id=main')
    expect(seen[0]?.method).toBe('GET')
    expect(status.state).toBe('pending')
  })

  test('mcpAuthComplete POSTs {code, state} to the complete route', async () => {
    const seen = captureFetch({ flow_id: 'f1', server: 'srv', state: 'done', authenticated: true })
    const status = await api().mcpAuthComplete('main', 'f1', { code: 'c0de', state: 'st4te' })

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp/auth/f1/complete?agent_id=main')
    expect(seen[0]?.method).toBe('POST')
    expect(JSON.parse(String(seen[0]?.body))).toEqual({ code: 'c0de', state: 'st4te' })
    expect(status.authenticated).toBe(true)
  })

  test('mcpAuthComplete without a state sends it undefined (server default)', async () => {
    const seen = captureFetch({ flow_id: 'f1', server: 'srv', state: 'done', authenticated: true })
    await api().mcpAuthComplete('main', 'f1', { code: 'c0de' })

    expect(JSON.parse(String(seen[0]?.body))).toEqual({ code: 'c0de' })
  })

  test('mcpAuthCancel DELETEs the flow route and resolves undefined on 204', async () => {
    const seen = captureFetch204()
    const result = await api().mcpAuthCancel('main', 'f1')

    expect(seen[0]?.url).toBe('http://127.0.0.1:18793/api/v1/mcp/auth/f1?agent_id=main')
    expect(seen[0]?.method).toBe('DELETE')
    expect(result).toBeUndefined()
  })

  test('mcpLogout DELETEs the server auth route and resolves undefined on 204', async () => {
    const seen = captureFetch204()
    const result = await api().mcpLogout('a b', 'srv/ice')

    expect(seen[0]?.url).toBe(
      'http://127.0.0.1:18793/api/v1/mcp/servers/srv%2Fice/auth?agent_id=a%20b',
    )
    expect(seen[0]?.method).toBe('DELETE')
    expect(result).toBeUndefined()
  })

  test('a rejected MCP write surfaces the server error code', async () => {
    globalThis.fetch = mock(
      async () =>
        new Response(
          JSON.stringify({ error: 'shadowed by layer "project"', code: 'mcp_entry_shadowed' }),
          { status: 409, headers: { 'Content-Type': 'application/json' } },
        ),
    ) as unknown as typeof fetch

    await expect(api().mcpToggle('main', 'agent', 'srv', false)).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      code: 'mcp_entry_shadowed',
    })
  })
})

describe('refresh failure and session survival', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  const json = (payload: unknown, status = 200, headers: Record<string, string> = {}) =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json', ...headers },
    })

  function mockServer(opts: {
    refreshes: (() => Response | Error | null)[]
    deny?: number
    accept?: string[]
  }) {
    const calls: { url: string; auth: string | null }[] = []
    let denied = 0
    let refreshTaken = 0
    const deny = opts.deny ?? 1
    const accept = opts.accept ?? []

    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const auth = (init?.headers as Record<string, string> | undefined)?.Authorization ?? null
      calls.push({ url, auth })

      if (url.endsWith('/auth/refresh')) {
        const next = opts.refreshes[Math.min(refreshTaken++, opts.refreshes.length - 1)]()
        if (next instanceof Error) throw next
        if (next === null) throw new TypeError('Failed to fetch')
        return next
      }

      if (accept.includes(auth?.replace(/^Bearer /, '') ?? '')) {
        return json({ agent_id: 'coder', tools: [], skills: [] })
      }
      if (denied < deny) {
        denied++
        return json({ error: 'unauthorized', code: 'auth_error' }, 401)
      }
      return json({ agent_id: 'coder', tools: [], skills: [] })
    }) as unknown as typeof fetch

    return {
      calls,
      refreshCount: () => calls.filter((c) => c.url.endsWith('/auth/refresh')).length,
    }
  }

  function client(refreshToken = 'refresh-1') {
    const api = createApiClient('http://127.0.0.1:18793')
    let failed = false
    api.setToken('access-1', refreshToken)
    api.setAuthFailureHandler(() => {
      failed = true
    })
    return { api, wasLoggedOut: () => failed }
  }

  test('classifies only an explicit auth rejection as fatal', () => {
    expect(isFatalRefreshFailure(400, 'refresh_error')).toBe(true)
    expect(isFatalRefreshFailure(401, 'refresh_error')).toBe(true)
    expect(isFatalRefreshFailure(429, 'rate_limit_exceeded')).toBe(false)
    expect(isFatalRefreshFailure(500, undefined)).toBe(false)
    expect(isFatalRefreshFailure(502, undefined)).toBe(false)
    expect(isFatalRefreshFailure(0, undefined)).toBe(false)
    expect(isFatalRefreshFailure(400, 'body_invalid')).toBe(false)
    expect(isFatalRefreshFailure(400, undefined)).toBe(false)
  })

  test('a 429 on refresh keeps the session and does not log out', async () => {
    const server = mockServer({
      refreshes: [() => json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429)],
      deny: 999,
    })
    const { api, wasLoggedOut } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)

    expect(server.refreshCount()).toBe(1)
    expect(wasLoggedOut()).toBe(false)
  })

  test('a 5xx on refresh keeps the session', async () => {
    mockServer({ refreshes: [() => json({ error: 'boom' }, 500)], deny: 999 })
    const { api, wasLoggedOut } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(wasLoggedOut()).toBe(false)
  })

  test('a dropped refresh request keeps the session', async () => {
    mockServer({ refreshes: [() => null], deny: 999 })
    const { api, wasLoggedOut } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(wasLoggedOut()).toBe(false)
  })

  test('a transient failure blocks further refresh attempts (no self-amplification)', async () => {
    const server = mockServer({
      refreshes: [() => json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429)],
      deny: 999,
    })
    const { api } = client()

    for (let i = 0; i < 5; i++) {
      await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    }

    expect(server.refreshCount()).toBe(1)
  })

  test('concurrent 401s share a single refresh attempt', async () => {
    const server = mockServer({
      refreshes: [() => json({ token: 'access-2', refresh_token: 'refresh-2' })],
      deny: 999,
    })
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('access-1', 'refresh-1')

    await Promise.allSettled([api.getAgentCatalog('a'), api.getAgentCatalog('b')])

    expect(server.refreshCount()).toBe(1)
  })

  test('a zero-second Retry-After does not switch the backoff off', async () => {
    const server = mockServer({
      refreshes: [
        () =>
          json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429, {
            'Retry-After': '0',
          }),
      ],
      deny: 999,
    })
    const { api } = client()

    for (let i = 0; i < 3; i++) {
      await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    }
    expect(server.refreshCount()).toBe(1)
  })

  test('a huge Retry-After is clamped instead of freezing renewal', async () => {
    const server = mockServer({
      refreshes: [
        () =>
          json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429, {
            'Retry-After': '999999999',
          }),
      ],
      deny: 999,
    })
    const { api } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(server.refreshCount()).toBe(1)
  })

  test('a successful refresh retries the original request with the new token', async () => {
    const server = mockServer({
      refreshes: [() => json({ token: 'access-2', refresh_token: 'refresh-2' })],
    })
    const { api, wasLoggedOut } = client()

    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(wasLoggedOut()).toBe(false)
    const retried = server.calls.filter(
      (c) => !c.url.endsWith('/auth/refresh') && c.auth === 'Bearer access-2',
    )
    expect(retried).toHaveLength(1)
  })

  test('an expired credential logs out exactly once', async () => {
    const server = mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      deny: 999,
    })
    const { api, wasLoggedOut } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(server.refreshCount()).toBe(1)
    expect(wasLoggedOut()).toBe(true)
  })

  test('adopts a session another tab rotated instead of logging out', async () => {
    saveSession({
      client_id: 'client-1',
      device_name: 'other-tab',
      token: 'access-from-other-tab',
      refresh_token: 'refresh-from-other-tab',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    const server = mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
    })
    const { api, wasLoggedOut } = client()

    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(wasLoggedOut()).toBe(false)
    expect(server.calls.some((c) => c.auth === 'Bearer access-from-other-tab')).toBe(true)
    expect(loadSession()?.refresh_token).toBe('refresh-from-other-tab')
  })

  test('adopts a rotation made by another tab of the same client', async () => {
    saveSession({
      client_id: 'client-MINE',
      device_name: 'My Desktop',
      token: 'access-rotated',
      refresh_token: 'refresh-rotated',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    const server = mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      accept: ['access-rotated'],
    })

    const api = createApiClient('http://127.0.0.1:18793')
    let failed = false
    api.setToken('access-1', 'refresh-1', undefined, 'client-MINE')
    api.setAuthFailureHandler(() => {
      failed = true
    })

    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(failed).toBe(false)
    expect(server.calls.some((c) => c.auth === 'Bearer access-rotated')).toBe(true)
  })

  test('refuses to adopt a session belonging to a different client', async () => {
    saveSession({
      client_id: 'client-OTHER',
      device_name: 'some other device',
      token: 'access-of-other-client',
      refresh_token: 'refresh-of-other-client',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    const server = mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      deny: 999,
      accept: ['access-of-other-client'],
    })

    const api = createApiClient('http://127.0.0.1:18793')
    let failed = false
    api.setToken('access-1', 'refresh-1', undefined, 'client-MINE')
    api.setAuthFailureHandler(() => {
      failed = true
    })

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)

    expect(failed).toBe(true)
    expect(server.calls.some((c) => c.auth === 'Bearer access-of-other-client')).toBe(false)
    expect(loadSession()?.client_id).toBe('client-OTHER')
  })

  test('adoption happens once: a second rejection ends the session', async () => {
    saveSession({
      client_id: 'client-1',
      device_name: 'other-tab',
      token: 'access-from-other-tab',
      refresh_token: 'refresh-from-other-tab',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    const server = mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      deny: 999,
    })
    const api = createApiClient('http://127.0.0.1:18793')
    let failed = 0
    api.setToken('access-1', 'refresh-1')
    api.setAuthFailureHandler(() => {
      failed++
    })

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(failed).toBe(0)
    expect(server.refreshCount()).toBe(0)

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(failed).toBe(1)
    expect(server.refreshCount()).toBe(1)

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(failed).toBe(1)
    expect(server.calls.at(-1)?.auth).toBeNull()
  })

  test("the cooldown does not block adopting another tab's rotation", async () => {
    mockServer({
      refreshes: [() => json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429)],
      deny: 999,
      accept: ['access-from-other-tab'],
    })
    const { api, wasLoggedOut } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)

    saveSession({
      client_id: 'client-1',
      device_name: 'other-tab',
      token: 'access-from-other-tab',
      refresh_token: 'refresh-from-other-tab',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    const catalog = await api.getAgentCatalog('coder')
    expect(catalog.agent_id).toBe('coder')
    expect(wasLoggedOut()).toBe(false)
  })

  test('a new credential does not inherit the previous cooldown', async () => {
    const server = mockServer({
      refreshes: [
        () => json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429),
        () => json({ token: 'access-2', refresh_token: 'refresh-2' }),
      ],
      deny: 999,
    })
    const { api } = client()

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(server.refreshCount()).toBe(1)

    api.setToken('access-new', 'refresh-new')
    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(server.refreshCount()).toBe(2)
  })

  test('a rejection with no stored session ends the session immediately', async () => {
    localStorage.clear()
    mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      deny: 999,
    })
    const api = createApiClient('http://127.0.0.1:18793')
    const wipe: boolean[] = []
    api.setToken('access-1', 'refresh-1')
    api.setAuthFailureHandler((mayClearStorage) => {
      wipe.push(mayClearStorage)
    })

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    expect(wipe).toEqual([true])
  })

  const foreignSession = {
    client_id: 'client-OTHER',
    device_name: 'some other device',
    token: 'access-of-other-client',
    refresh_token: 'refresh-of-other-client',
    expires: new Date(Date.now() + 3_600_000).toISOString(),
  }

  test("a fatal refresh leaves another client's stored session alone", async () => {
    saveSession(foreignSession)

    mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      deny: 999,
    })

    const api = createApiClient('http://127.0.0.1:18793')
    const wipe: boolean[] = []
    api.setToken('access-1', 'refresh-1', undefined, 'client-MINE')
    api.setAuthFailureHandler((mayClearStorage) => {
      wipe.push(mayClearStorage)
    })

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)

    expect(wipe).toEqual([false])
    expect(api.getToken()).toBeNull()
    expect(loadSession()?.client_id).toBe('client-OTHER')
    expect(loadSession()?.refresh_token).toBe('refresh-of-other-client')
  })

  test('a fatal refresh may erase storage when the stored session is ours', async () => {
    saveSession({
      client_id: 'client-MINE',
      device_name: 'My Desktop',
      token: 'access-1',
      refresh_token: 'refresh-1',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    mockServer({
      refreshes: [() => json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)],
      deny: 999,
    })

    const api = createApiClient('http://127.0.0.1:18793')
    const wipe: boolean[] = []
    api.setToken('access-1', 'refresh-1', undefined, 'client-MINE')
    api.setAuthFailureHandler((mayClearStorage) => {
      wipe.push(mayClearStorage)
    })

    await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)

    expect(wipe).toEqual([true])
    expect(api.getToken()).toBeNull()
  })

  test('adopts a rotation that lands during the second adoption look', async () => {
    saveSession({
      client_id: 'client-MINE',
      device_name: 'My Desktop',
      token: 'access-1',
      refresh_token: 'refresh-1',
      expires: new Date(Date.now() + 3_600_000).toISOString(),
    })

    const winnerDelayMs = 100
    const seen: (string | null)[] = []
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/auth/refresh')) {
        setTimeout(() => {
          saveSession({
            client_id: 'client-MINE',
            device_name: 'My Desktop',
            token: 'access-from-winner',
            refresh_token: 'refresh-from-winner',
            expires: new Date(Date.now() + 3_600_000).toISOString(),
          })
        }, winnerDelayMs)
        return json({ error: 'invalid refresh token', code: 'refresh_error' }, 400)
      }

      const auth = (init?.headers as Record<string, string> | undefined)?.Authorization ?? null
      seen.push(auth)
      if (auth === 'Bearer access-from-winner') {
        return json({ agent_id: 'coder', tools: [], skills: [] })
      }
      return json({ error: 'unauthorized', code: 'auth_error' }, 401)
    }) as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    const wipe: boolean[] = []
    api.setToken('access-1', 'refresh-1', undefined, 'client-MINE')
    api.setAuthFailureHandler((mayClearStorage) => {
      wipe.push(mayClearStorage)
    })

    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(wipe).toEqual([])
    expect(seen).toContain('Bearer access-from-winner')
    expect(loadSession()?.refresh_token).toBe('refresh-from-winner')
  })
})
describe('the renewed credential carries the server deadline', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  const json = (payload: unknown, status = 200) =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })

  function mockOnceExpiredRefresh(body: () => unknown) {
    let denied = 0
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/auth/refresh')) {
        return json(body())
      }
      if (denied++ === 0) {
        return json({ error: 'unauthorized', code: 'auth_error' }, 401)
      }
      const auth = (init?.headers as Record<string, string> | undefined)?.Authorization
      return json({ agent_id: 'coder', tools: [], skills: [], auth })
    }) as unknown as typeof fetch
  }

  function trackingClient() {
    const api = createApiClient('http://127.0.0.1:18793')
    const persisted: AuthSession[] = []
    api.setToken('access-1', 'refresh-1', (next) => {
      persisted.push(next)
      saveSession({ ...next, client_id: 'client-1', device_name: 'My Desktop' })
    })
    return { api, persisted }
  }

  test('persists the expires the server answered with', async () => {
    const serverExpires = new Date(Date.now() + 30 * 24 * 3600 * 1000).toISOString()
    mockOnceExpiredRefresh(() => ({
      token: 'access-2',
      refresh_token: 'refresh-2',
      expires: serverExpires,
    }))

    const { api, persisted } = trackingClient()
    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(persisted).toHaveLength(1)
    expect(persisted[0]?.expires).toBe(serverExpires)
    expect(loadSession()?.expires).toBe(serverExpires)
    expect(loadSession()?.token).toBe('access-2')
    expect(loadSession()?.refresh_token).toBe('refresh-2')
  })

  test('keeps the old near-future fallback when the answer has no usable expires', async () => {
    const before = Date.now()
    mockOnceExpiredRefresh(() => ({ token: 'access-2', refresh_token: 'refresh-2' }))

    const { api, persisted } = trackingClient()
    await api.getAgentCatalog('coder')

    const fallback = Date.parse(persisted[0]?.expires ?? '')
    expect(Number.isFinite(fallback)).toBe(true)
    expect(fallback - before).toBeGreaterThan(0)
    expect(fallback - before).toBeLessThan(3600_000 + 5_000)
  })

  test('also rejects a malformed expires instead of storing an unparsable deadline', async () => {
    mockOnceExpiredRefresh(() => ({
      token: 'access-2',
      refresh_token: 'refresh-2',
      expires: 'not-a-date',
    }))

    const { api, persisted } = trackingClient()
    await api.getAgentCatalog('coder')

    expect(Number.isFinite(Date.parse(persisted[0]?.expires ?? ''))).toBe(true)
  })
})

describe('refreshNow', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  const json = (payload: unknown, status = 200, headers: Record<string, string> = {}) =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json', ...headers },
    })

  function mockRefresh(answers: (() => Response)[]) {
    const calls: string[] = []
    let taken = 0
    globalThis.fetch = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      return answers[Math.min(taken++, answers.length - 1)]()
    }) as unknown as typeof fetch
    return {
      refreshCount: () => calls.filter((url) => url.endsWith('/auth/refresh')).length,
    }
  }

  test('rotates the session and returns the new access token', async () => {
    const server = mockRefresh([
      () =>
        json({ token: 'access-2', refresh_token: 'refresh-2', expires: '2026-10-23T12:00:00Z' }),
    ])
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('access-1', 'refresh-1')

    expect(await api.refreshNow()).toBe('access-2')
    expect(api.getToken()).toBe('access-2')
    expect(server.refreshCount()).toBe(1)
  })

  test('concurrent proactive refreshes share a single rotation', async () => {
    const server = mockRefresh([
      () =>
        json({ token: 'access-2', refresh_token: 'refresh-2', expires: '2026-10-23T12:00:00Z' }),
    ])
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('access-1', 'refresh-1')

    const tokens = await Promise.all([api.refreshNow(), api.refreshNow()])

    expect(tokens).toEqual(['access-2', 'access-2'])
    expect(server.refreshCount()).toBe(1)
  })

  test('a transient failure returns null and leaves the session untouched', async () => {
    mockRefresh([() => json({ error: 'too many requests', code: 'rate_limit_exceeded' }, 429)])
    const api = createApiClient('http://127.0.0.1:18793')
    const wipes: boolean[] = []
    api.setToken('access-1', 'refresh-1')
    api.setAuthFailureHandler((mayClearStorage) => wipes.push(mayClearStorage))

    expect(await api.refreshNow()).toBeNull()
    expect(api.getToken()).toBe('access-1')
    expect(wipes).toEqual([])
  })

  test('returns null when there is no refresh token to spend', async () => {
    const server = mockRefresh([])
    const api = createApiClient('http://127.0.0.1:18793')

    expect(await api.refreshNow()).toBeNull()
    expect(server.refreshCount()).toBe(0)
  })
})

describe('a 401 for a superseded credential', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  const json = (payload: unknown, status = 200) =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })

  function mockServer(opts: {
    accepted: string[]
    refreshBody?: () => unknown
    onAttempt?: (call: { url: string; auth: string | null }) => void
  }) {
    const calls: { url: string; auth: string | null }[] = []
    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const auth = (init?.headers as Record<string, string> | undefined)?.Authorization ?? null
      const call = { url, auth }
      calls.push(call)

      if (url.endsWith('/auth/refresh')) {
        return json(opts.refreshBody?.() ?? {})
      }

      opts.onAttempt?.(call)
      const bearer = auth?.replace(/^Bearer /, '') ?? ''
      if (opts.accepted.includes(bearer)) {
        return json({ agent_id: 'coder', tools: [], skills: [] })
      }
      return json({ error: 'unauthorized', code: 'auth_error' }, 401)
    }) as unknown as typeof fetch

    return {
      calls,
      refreshCount: () => calls.filter((c) => c.url.endsWith('/auth/refresh')).length,
      nonAuth: () => calls.filter((c) => !c.url.endsWith('/auth/refresh')),
    }
  }

  test('a 401 for a token a rotation already replaced is replayed without a second refresh', async () => {
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('access-1', 'refresh-1')

    let rotated = false
    const server = mockServer({
      accepted: ['access-2', 'access-3'],
      refreshBody: () => ({ token: 'access-3', refresh_token: 'refresh-3' }),
      onAttempt: () => {
        if (rotated) return
        rotated = true
        api.setToken('access-2', 'refresh-2')
      },
    })

    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(server.nonAuth().map((c) => c.auth)).toEqual(['Bearer access-1', 'Bearer access-2'])
    expect(server.refreshCount()).toBe(0)
    expect(server.calls).toHaveLength(2)
  })

  test('a superseded replay does not hide a dead credential', async () => {
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('access-1', 'refresh-1')

    let rotated = false
    const server = mockServer({
      accepted: ['access-3'],
      refreshBody: () => ({ token: 'access-3', refresh_token: 'refresh-3' }),
      onAttempt: () => {
        if (rotated) return
        rotated = true
        api.setToken('access-2', 'refresh-2')
      },
    })

    const catalog = await api.getAgentCatalog('coder')

    expect(catalog.agent_id).toBe('coder')
    expect(server.refreshCount()).toBe(1)
    expect(server.nonAuth().map((c) => c.auth)).toEqual([
      'Bearer access-1',
      'Bearer access-2',
      'Bearer access-3',
    ])
    const refreshIndex = server.calls.findIndex((c) => c.url.endsWith('/auth/refresh'))
    const replayIndex = server.calls.findIndex((c) => c.auth === 'Bearer access-2')
    expect(replayIndex).toBeGreaterThan(-1)
    expect(refreshIndex).toBeGreaterThan(replayIndex)
  })
})
