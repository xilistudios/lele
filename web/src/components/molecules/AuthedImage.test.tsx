import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { render, waitFor } from '@testing-library/react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { AuthedImage, clearAuthedImageCache } from './AuthedImage'

const originalCreateObjectURL = URL.createObjectURL
const originalRevokeObjectURL = URL.revokeObjectURL
const originalIntersectionObserver = globalThis.IntersectionObserver

function makeAuthCtx(fileBlobMock: ReturnType<typeof mock>) {
  return {
    api: { fileBlob: fileBlobMock },
    apiUrl: 'http://127.0.0.1:18793',
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => ({ token: '', refresh_token: '', device_name: '', client_id: '' }),
    ensureSession: async () => null,
    isLoading: false,
  } as unknown as AuthContextValue
}

function renderWithAuth(ui: React.ReactElement, ctx: AuthContextValue) {
  return render(<AuthContext.Provider value={ctx}>{ui}</AuthContext.Provider>)
}

beforeEach(() => {
  clearAuthedImageCache()

  URL.createObjectURL = mock((_blob: Blob) => 'blob:test-url/abc') as typeof URL.createObjectURL
  URL.revokeObjectURL = mock(() => {}) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
  globalThis.IntersectionObserver = originalIntersectionObserver
})

describe('AuthedImage', () => {
  test('loads immediately without IntersectionObserver and renders <img>', async () => {
    // Remove IntersectionObserver to simulate jsdom
    // @ts-expect-error removing for test
    globalThis.IntersectionObserver = undefined

    const blob = new Blob(['fake-png'], { type: 'image/png' })
    const fileBlob = mock(async () => blob)
    const ctx = makeAuthCtx(fileBlob)

    const { container } = renderWithAuth(
      <AuthedImage
        path="/home/user/.lele/wk/attachments/20260919/abc_def_photo.png"
        alt="photo.png"
      />,
      ctx,
    )

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
      expect(img?.getAttribute('src')).toBe('blob:test-url/abc')
    })

    expect(fileBlob).toHaveBeenCalledTimes(1)
    expect(fileBlob).toHaveBeenCalledWith(
      '/home/user/.lele/wk/attachments/20260919/abc_def_photo.png',
    )
  })

  test('delays loading until IntersectionObserver fires', async () => {
    let savedCallback: ((entries: unknown[]) => void) | null = null
    let disconnected = false

    class FakeIntersectionObserver {
      constructor(cb: (entries: unknown[]) => void) {
        savedCallback = cb
      }
      observe() {}
      disconnect() {
        disconnected = true
      }
      unobserve() {}
      takeRecords() {
        return []
      }
    }

    globalThis.IntersectionObserver =
      FakeIntersectionObserver as unknown as typeof IntersectionObserver

    const blob = new Blob(['fake-png'], { type: 'image/png' })
    const fileBlob = mock(async () => blob)
    const ctx = makeAuthCtx(fileBlob)

    const { container } = renderWithAuth(
      <AuthedImage
        path="/home/user/.lele/wk/attachments/20260919/abc_def_photo.png"
        alt="photo.png"
      />,
      ctx,
    )

    // fileBlob should NOT have been called yet
    expect(fileBlob).toHaveBeenCalledTimes(0)

    // Should show loading state
    const loadingEl = container.querySelector('[data-state="loading"]')
    expect(loadingEl).toBeTruthy()

    // Simulate intersection
    const cb = savedCallback as ((entries: unknown[]) => void) | null
    cb?.([{ isIntersecting: true, target: container.firstChild }])

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
    })

    expect(fileBlob).toHaveBeenCalledTimes(1)
    expect(disconnected).toBe(true)
  })

  test('shows error chip when fileBlob rejects', async () => {
    // @ts-expect-error removing for test
    globalThis.IntersectionObserver = undefined

    const fileBlob = mock(async () => {
      throw new Error('access denied')
    })
    const ctx = makeAuthCtx(fileBlob)

    const { container } = renderWithAuth(
      <AuthedImage
        path="/home/user/.lele/wk/attachments/20260919/abc_def_photo.png"
        alt="photo.png"
      />,
      ctx,
    )

    await waitFor(() => {
      const errorEl = container.querySelector('[data-state="error"]')
      expect(errorEl).toBeTruthy()
      expect(errorEl?.textContent).toBe('abc_def_photo.png')
    })

    expect(container.querySelector('img')).toBeNull()
  })

  test('in-flight dedupe: two simultaneously mounted AuthedImage with same path → fileBlob called once', async () => {
    // @ts-expect-error removing for test
    globalThis.IntersectionObserver = undefined

    const blob = new Blob(['fake-png'], { type: 'image/png' })
    const fileBlob = mock(async () => blob)
    const ctx = makeAuthCtx(fileBlob)

    const { container } = renderWithAuth(
      <div>
        <AuthedImage path="/home/user/.lele/wk/attachments/20260919/abc_def_photo.png" alt="a" />
        <AuthedImage path="/home/user/.lele/wk/attachments/20260919/abc_def_photo.png" alt="b" />
      </div>,
      ctx,
    )

    await waitFor(() => {
      const imgs = container.querySelectorAll('img')
      expect(imgs.length).toBe(2)
    })

    // fileBlob should only be called once due to deduplication
    expect(fileBlob).toHaveBeenCalledTimes(1)
  })

  test('cache: unmount then remount with same path → served from cache, fileBlob called once', async () => {
    // @ts-expect-error removing for test
    globalThis.IntersectionObserver = undefined

    const blob = new Blob(['fake-png'], { type: 'image/png' })
    const fileBlob = mock(async () => blob)
    const ctx = makeAuthCtx(fileBlob)
    const path = '/home/user/.lele/wk/attachments/20260919/abc_def_photo.png'

    // --- First render: triggers a real fetch ---
    const { container, unmount } = renderWithAuth(<AuthedImage path={path} alt="a" />, ctx)

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
      expect(img?.getAttribute('src')).toBe('blob:test-url/abc')
    })

    expect(fileBlob).toHaveBeenCalledTimes(1)

    unmount()

    // --- Second render: same path, module-level cache must still hold the entry ---
    const { container: container2 } = renderWithAuth(<AuthedImage path={path} alt="a" />, ctx)

    await waitFor(() => {
      const img2 = container2.querySelector('img')
      expect(img2).toBeTruthy()
      expect(img2?.getAttribute('src')).toBe('blob:test-url/abc')
    })

    // fileBlob must STILL have been called exactly once (cache hit, no refetch)
    expect(fileBlob).toHaveBeenCalledTimes(1)
  })

  test('empty path renders error chip without fetching', () => {
    // @ts-expect-error removing for test
    globalThis.IntersectionObserver = undefined

    const fileBlob = mock(async () => new Blob())
    const ctx = makeAuthCtx(fileBlob)

    const { container } = renderWithAuth(<AuthedImage path="" alt="missing.png" />, ctx)

    const errorEl = container.querySelector('[data-state="error"]')
    expect(errorEl).toBeTruthy()
    expect(errorEl?.textContent).toBe('missing.png')
    expect(fileBlob).toHaveBeenCalledTimes(0)
  })

  test('retries fetch after a transient failure (inflight cleared on error)', async () => {
    // @ts-expect-error removing for test
    globalThis.IntersectionObserver = undefined

    let callCount = 0
    const fileBlob = mock(async () => {
      callCount++
      if (callCount === 1) throw new Error('transient network error')
      return new Blob(['retry-ok'], { type: 'image/png' })
    })
    const ctx = makeAuthCtx(fileBlob)

    const path = '/home/user/.lele/wk/attachments/20260919/retry_photo.png'

    // --- First render: fileBlob rejects on call #1 ---
    const { container, unmount } = renderWithAuth(<AuthedImage path={path} alt="retry.png" />, ctx)

    await waitFor(() => {
      const errorEl = container.querySelector('[data-state="error"]')
      expect(errorEl).toBeTruthy()
    })

    expect(container.querySelector('img')).toBeNull()
    expect(fileBlob).toHaveBeenCalledTimes(1)

    unmount()

    // --- Second render: same path, fileBlob now resolves on call #2 ---
    const { container: container2 } = renderWithAuth(
      <AuthedImage path={path} alt="retry.png" />,
      ctx,
    )

    await waitFor(() => {
      const img = container2.querySelector('img')
      expect(img).toBeTruthy()
      expect(img?.getAttribute('src')).toBe('blob:test-url/abc')
    })

    // fileBlob must have been called twice (once per render)
    expect(fileBlob).toHaveBeenCalledTimes(2)
    expect(callCount).toBe(2)
  })
})
