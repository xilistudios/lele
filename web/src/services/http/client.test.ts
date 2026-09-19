import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { loadSession, saveSession } from '../../lib/storage'
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

/**
 * Per-agent skill administration (workspace writes).
 *
 * The routes themselves are covered server-side in
 * `pkg/channels/rest_agent_skills_test.go`; what is covered here is the wire
 * contract the frontend depends on: WHICH path each call hits, WHICH verb, and
 * that the body carries the fields the handler decodes. A typo in `scope` or a
 * missing `enabled` is invisible to the component tests (they stub this layer),
 * so it has to be pinned at the client.
 */
describe('per-agent skill mutations', () => {
  /** Captures the last request as {url, method, body}. */
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
    // Skill names come from disk and agent ids are user-authored: a name with
    // a slash must not escape the route it belongs to.
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
    // Omitting must mean "whatever the server defaults to" (workspace), not
    // "global": the JSON key is simply absent.
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
    // The delete guard (global/builtin skills) answers 400 with a code; the
    // panel prints `error.message`, so the message must survive this layer.
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
})

/**
 * Per-agent slash commands (brief §6 wire contract).
 *
 * Same rationale as the skill-mutation block above: the component tests stub
 * this layer, so WHICH path/verb/body each call hits must be pinned here.
 * `content` is the full markdown file (frontmatter serialised by the frontend,
 * parsed by the Go handler), and `allow_absolute_files` is tri-state — null
 * means "inherit", so the client must move whatever the caller gives it.
 */
describe('per-agent command endpoints', () => {
  /** Captures the last request as {url, method, body}. */
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
    // 403 not_editable (config/directory level) must survive this layer: the
    // dialog prints error.message.
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

/**
 * Session survival on refresh failure.
 *
 * The refresh token is single-use and the endpoint is rate limited, so the
 * answer to a refresh attempt is not always "this credential is dead": a 429,
 * a 5xx or a dropped request all leave the credential untouched. Before this,
 * every one of those wiped the session, and because localStorage is shared by
 * all tabs a single transient failure logged the whole browser out.
 */
describe('refresh failure and session survival', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  const json = (payload: unknown, status = 200, headers: Record<string, string> = {}) =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json', ...headers },
    })

  /**
   * Protected endpoint answers 401 for the first `deny` calls, then succeeds.
   * `refreshes` are handed out in order; an Error/null entry simulates a
   * request that never got an answer.
   */
  function mockServer(opts: { refreshes: (() => Response | Error | null)[]; deny?: number }) {
    const calls: { url: string; auth: string | null }[] = []
    let denied = 0
    let refreshTaken = 0
    const deny = opts.deny ?? 1

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
    // Rate limit, server errors, network (status 0) and unrelated 400s must
    // never destroy a session.
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

    // Several requests in a row must not keep hammering a limited endpoint:
    // the access token is still the rejected one, so each retry would trigger
    // yet another refresh and deepen the rate-limit hole.
    for (let i = 0; i < 5; i++) {
      await expect(api.getAgentCatalog('coder')).rejects.toBeInstanceOf(ApiError)
    }

    expect(server.refreshCount()).toBe(1)
  })

  test('concurrent 401s share a single refresh attempt', async () => {
    // The refresh token is single-use: two parallel rotations would make the
    // second one look like a revoked credential and log the session out.
    const server = mockServer({
      refreshes: [() => json({ token: 'access-2', refresh_token: 'refresh-2' })],
      deny: 999,
    })
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('access-1', 'refresh-1')

    await Promise.allSettled([api.getAgentCatalog('a'), api.getAgentCatalog('b')])

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
    // Two tabs held the same refresh token; the other one already rotated it
    // and persisted the result. Our copy is now rejected as invalid, but the
    // browser session is very much alive.
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
    // The stale token must not be written back over the fresh one.
    expect(loadSession()?.refresh_token).toBe('refresh-from-other-tab')
  })
})
