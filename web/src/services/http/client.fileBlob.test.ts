import { afterEach, describe, expect, mock, test } from 'bun:test'
import { createApiClient } from './client'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
})

describe('fileBlob', () => {
  const makeApi = () => {
    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('my-token', 'my-refresh')
    return api
  }

  test('builds the correct URL with encoded path and sends Authorization', async () => {
    const captured: { url: string; auth: string | null }[] = []
    const fileContent = new Blob(['hello world'], { type: 'image/jpeg' })

    globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
      captured.push({
        url: String(input),
        auth: (init?.headers as Record<string, string>)?.Authorization ?? null,
      })
      return new Response(fileContent, {
        status: 200,
        headers: { 'Content-Type': 'image/jpeg' },
      })
    }) as unknown as typeof fetch

    const api = makeApi()
    const path = '/home/user/.lele/workspace-agent/attachments/20260919/abc_def_My File+Shot.jpg'
    await api.fileBlob(path)

    expect(captured).toHaveLength(1)
    expect(captured[0]?.url).toBe(
      `http://127.0.0.1:18793/api/v1/files/view-secure?path=${encodeURIComponent(path)}`,
    )
    expect(captured[0]?.auth).toBe('Bearer my-token')
  })

  test('returns a Blob with the correct content', async () => {
    const content = 'binary-image-data-here'
    globalThis.fetch = mock(
      async () =>
        new Response(new Blob([content], { type: 'image/png' }), {
          status: 200,
          headers: { 'Content-Type': 'image/png' },
        }),
    ) as unknown as typeof fetch

    const api = makeApi()
    const blob = await api.fileBlob('/some/file.png')

    expect(blob).toBeInstanceOf(Blob)
    expect(await blob.text()).toBe(content)
  })

  test('retries once on 401 with refresh and returns blob on success', async () => {
    let callIndex = 0
    const capturedAuth: string[] = []

    globalThis.fetch = mock(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const auth = (init?.headers as Record<string, string>)?.Authorization ?? 'none'
      capturedAuth.push(auth)
      callIndex++

      if (String(_input).includes('/auth/refresh')) {
        return new Response(JSON.stringify({ token: 'new-access', refresh_token: 'new-refresh' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }

      // First file request → 401, second → 200
      if (callIndex === 1) {
        return new Response(JSON.stringify({ error: 'unauthorized', code: 'auth_error' }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      return new Response(new Blob(['recovered']), {
        status: 200,
        headers: { 'Content-Type': 'application/octet-stream' },
      })
    }) as unknown as typeof fetch

    const api = makeApi()
    const blob = await api.fileBlob('/some/path.bin')

    expect(blob).toBeInstanceOf(Blob)
    expect(await blob.text()).toBe('recovered')

    // First call had old token, second had refreshed token
    expect(capturedAuth[0]).toBe('Bearer my-token')
    // The refresh call happens in the middle, so index 2 is the retry
    expect(capturedAuth[2]).toBe('Bearer new-access')
  })

  test('rejects with ApiError on 403 without retrying', async () => {
    let fetchCallCount = 0

    globalThis.fetch = mock(async () => {
      fetchCallCount++
      return new Response(
        JSON.stringify({ error: 'access_denied', code: 'access_denied', message: 'forbidden' }),
        { status: 403, headers: { 'Content-Type': 'application/json' } },
      )
    }) as unknown as typeof fetch

    const api = makeApi()
    await expect(api.fileBlob('/forbidden/path')).rejects.toMatchObject({
      name: 'ApiError',
      status: 403,
      code: 'access_denied',
    })

    // Only 1 fetch call — no refresh attempted (403 is not 401)
    expect(fetchCallCount).toBe(1)
  })

  test('rejects with ApiError on 404', async () => {
    globalThis.fetch = mock(async () => {
      return new Response(
        JSON.stringify({ error: 'not_found', code: 'not_found', message: 'file not found' }),
        { status: 404, headers: { 'Content-Type': 'application/json' } },
      )
    }) as unknown as typeof fetch

    const api = makeApi()
    await expect(api.fileBlob('/missing/file.txt')).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
      code: 'not_found',
    })
  })
})
