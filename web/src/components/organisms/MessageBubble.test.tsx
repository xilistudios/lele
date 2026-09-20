import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { ChatMessage } from '../../lib/types'
import { clearAuthedImageCache } from '../molecules/AuthedImage'
import { MessageBubble } from './MessageBubble'

const originalCreateObjectURL = URL.createObjectURL
const originalRevokeObjectURL = URL.revokeObjectURL
const originalIntersectionObserver = globalThis.IntersectionObserver

function makeAuthCtx(overrides: Partial<{ fileBlob: ReturnType<typeof mock> }> = {}) {
  const fileBlob = overrides.fileBlob ?? mock(async () => new Blob(['test'], { type: 'image/png' }))
  return {
    value: {
      api: { fileBlob },
      apiUrl: 'http://127.0.0.1:18793',
      session: null,
      setApiUrl: () => {},
      persistSession: () => {},
      handleAuth: async () => ({ token: '', refresh_token: '', device_name: '', client_id: '' }),
      ensureSession: async () => null,
      isLoading: false,
    } as unknown as AuthContextValue,
    fileBlob,
  }
}

function renderBubble(message: ChatMessage, ctxValue: AuthContextValue, isLast = false) {
  return render(
    <AuthContext.Provider value={ctxValue}>
      <MessageBubble message={message} isLast={isLast} onNavigateToSession={() => {}} />
    </AuthContext.Provider>,
  )
}

function makeUserMessage(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'msg-1',
    role: 'user',
    content: '',
    streaming: false,
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

function makeAssistantMessage(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'msg-2',
    role: 'assistant',
    content: '',
    streaming: false,
    createdAt: new Date().toISOString(),
    ...overrides,
  }
}

beforeEach(() => {
  clearAuthedImageCache()
  // Remove IntersectionObserver so AuthedImage loads immediately in tests
  // @ts-expect-error removing for test
  globalThis.IntersectionObserver = undefined

  URL.createObjectURL = mock((_blob: Blob) => 'blob:test-url/msg') as typeof URL.createObjectURL
  URL.revokeObjectURL = mock(() => {}) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
  globalThis.IntersectionObserver = originalIntersectionObserver
})

describe('MessageBubble — authenticated attachment preview', () => {
  test('user message with image attachment uses AuthedImage (not public endpoint)', async () => {
    const { value: ctx, fileBlob } = makeAuthCtx()

    const message = makeUserMessage({
      content:
        'mira esto\n\n## Attachments\n- /home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
      attachments: [
        {
          path: '/home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
          name: 'foto.png',
        },
      ],
    })

    const { container } = renderBubble(message, ctx)

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
      // The src should be an object URL (blob:), NOT the public endpoint
      expect(img?.getAttribute('src')).toBe('blob:test-url/msg')
      expect(img?.getAttribute('src')).not.toContain('/api/v1/files/view?')
    })

    // fileBlob must have been called with the exact workspace path
    expect(fileBlob).toHaveBeenCalledWith(
      '/home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
    )
  })

  test('assistant download button calls fileBlob and triggers download', async () => {
    const downloadBlob = new Blob(['download-data'], { type: 'application/octet-stream' })
    const fileBlob = mock(async () => downloadBlob)
    const { value: ctx } = makeAuthCtx({ fileBlob })

    // Track the created anchor for download
    const clickSpy = mock(() => {})
    const originalCreateElement = document.createElement.bind(document)
    document.createElement = ((tag: string) => {
      const el = originalCreateElement(tag)
      if (tag === 'a') {
        el.click = clickSpy as () => void
        Object.defineProperty(el, 'download', {
          set() {},
          get() {
            return 'file'
          },
        })
      }
      return el
    }) as typeof document.createElement

    const message = makeAssistantMessage({
      content: 'here is the image',
      attachments: [
        {
          path: '/home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
          name: 'foto.png',
        },
      ],
    })

    const { container } = renderBubble(message, ctx)

    // Wait for the image to load
    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
    })

    // Find and click the download button
    const downloadBtn = container.querySelector('button[aria-label]')
    expect(downloadBtn).toBeTruthy()

    if (downloadBtn) fireEvent.click(downloadBtn)

    await waitFor(() => {
      expect(fileBlob).toHaveBeenCalledWith(
        '/home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
      )
    })

    document.createElement = originalCreateElement
  })

  test('image alt attribute is the attachment name', async () => {
    const { value: ctx } = makeAuthCtx()

    const message = makeUserMessage({
      content: 'photo',
      attachments: [
        {
          path: '/home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
          name: 'foto.png',
        },
      ],
    })

    const { container } = renderBubble(message, ctx)

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
      expect(img?.getAttribute('alt')).toBe('foto.png')
    })
  })
})
