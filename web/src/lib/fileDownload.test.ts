import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { downloadFileViaApi, triggerBlobDownload } from './fileDownload'

let createdObjectUrl: string | null = null
let revokedUrls: string[] = []
const originalCreateObjectURL = URL.createObjectURL
const originalRevokeObjectURL = URL.revokeObjectURL

beforeEach(() => {
  createdObjectUrl = null
  revokedUrls = []
  URL.createObjectURL = mock((_blob: Blob) => {
    createdObjectUrl = 'blob:mock-url/123'
    return createdObjectUrl
  }) as typeof URL.createObjectURL
  URL.revokeObjectURL = mock((url: string) => {
    revokedUrls.push(url)
  }) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
})

describe('triggerBlobDownload', () => {
  test('creates an <a> with download attribute, clicks it, and removes it', () => {
    const clickSpy = mock(() => {})
    const originalCreateElement = document.createElement.bind(document)

    // Intercept createElement to spy on click for the <a> we create
    document.createElement = ((tag: string) => {
      const el = originalCreateElement(tag)
      if (tag === 'a') {
        el.click = clickSpy as () => void
      }
      return el
    }) as typeof document.createElement

    const blob = new Blob(['test'], { type: 'text/plain' })
    triggerBlobDownload(blob, 'myfile.txt')

    expect(clickSpy).toHaveBeenCalledTimes(1)
    expect(createdObjectUrl).toBe('blob:mock-url/123')
    // Object URL must be revoked even after a successful download
    expect(revokedUrls).toContain('blob:mock-url/123')

    document.createElement = originalCreateElement
  })

  test('uses "download" as fallback name when no name given', () => {
    let capturedName: string | undefined
    const originalCreateElement = document.createElement.bind(document)

    document.createElement = ((tag: string) => {
      const el = originalCreateElement(tag)
      if (tag === 'a') {
        el.click = mock(() => {}) as () => void
        // Intercept property set for download
        Object.defineProperty(el, 'download', {
          set(v: string) {
            capturedName = v
          },
          get() {
            return capturedName
          },
        })
      }
      return el
    }) as typeof document.createElement

    const blob = new Blob(['data'])
    triggerBlobDownload(blob)

    expect(capturedName).toBe('download')

    document.createElement = originalCreateElement
  })

  test('revokes the object URL even if click throws', () => {
    const originalCreateElement = document.createElement.bind(document)

    document.createElement = ((tag: string) => {
      const el = originalCreateElement(tag)
      if (tag === 'a') {
        el.click = () => {
          throw new Error('click failed')
        }
      }
      return el
    }) as typeof document.createElement

    const blob = new Blob(['data'])
    expect(() => triggerBlobDownload(blob, 'fail.txt')).toThrow('click failed')
    // URL is still revoked because of the finally block
    expect(revokedUrls).toContain('blob:mock-url/123')

    document.createElement = originalCreateElement
  })
})

describe('downloadFileViaApi', () => {
  test('calls fetcher.fileBlob with the path and triggers download with name', async () => {
    const blob = new Blob(['file-content'], { type: 'application/pdf' })
    const fetcher = {
      fileBlob: mock(async (_path: string) => blob),
    }

    const clickSpy = mock(() => {})
    const originalCreateElement = document.createElement.bind(document)
    document.createElement = ((tag: string) => {
      const el = originalCreateElement(tag)
      if (tag === 'a') {
        el.click = clickSpy as () => void
      }
      return el
    }) as typeof document.createElement

    await downloadFileViaApi(fetcher, '/workspace/attachments/report.pdf', 'report.pdf')

    expect(fetcher.fileBlob).toHaveBeenCalledWith('/workspace/attachments/report.pdf')
    expect(clickSpy).toHaveBeenCalledTimes(1)
    expect(createdObjectUrl).toBe('blob:mock-url/123')

    document.createElement = originalCreateElement
  })

  test('propagates errors from the fetcher', async () => {
    const fetcher = {
      fileBlob: mock(async () => {
        throw new Error('network down')
      }),
    }

    await expect(downloadFileViaApi(fetcher, '/path/file')).rejects.toThrow('network down')
  })
})
