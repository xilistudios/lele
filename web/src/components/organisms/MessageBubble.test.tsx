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
let savedOriginalCreateElement: typeof document.createElement | null = null

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
  if (savedOriginalCreateElement) {
    document.createElement = savedOriginalCreateElement
    savedOriginalCreateElement = null
  }
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
    const originalBound = document.createElement.bind(document)
    savedOriginalCreateElement = originalBound
    document.createElement = ((tag: string) => {
      const el = originalBound(tag)
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

    document.createElement = originalBound
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

describe('MessageBubble — system notice', () => {
  test('renderiza una card tipo tool, no la burbuja de texto plano', async () => {
    const { value: ctx } = makeAuthCtx()

    const message = makeUserMessage({
      content: '[System: subagent] Subagent status update.\nTask ID: t1',
      systemNotice: { sender: 'subagent', body: 'Subagent status update.\nTask ID: t1' },
    })

    const { container } = renderBubble(message, ctx)

    expect(container.querySelector('[data-system-notice="true"]')).toBeTruthy()
    // The raw "[System: subagent]" prefix must NOT leak as plain user text.
    expect(container.textContent).not.toContain('[System: subagent]')
    // The body starts collapsed.
    expect(container.textContent).not.toContain('Task ID: t1')

    const btn = container.querySelector('button[aria-expanded]')
    expect(btn).toBeTruthy()

    // (a) The collapsed card HEADER must show the notice SENDER, not the body.
    // MessageBubble wires `toolArgs={systemNotice.sender}` (header summary) and
    // `toolResult={systemNotice.body}` (expandable payload). Asserted before
    // expanding on purpose: this pins the sender→header wiring, so passing
    // `toolArgs={systemNotice.body}` (header showing the raw body) would now
    // fail instead of slipping through the suite.
    expect(btn?.textContent ?? '').toContain('subagent')

    if (btn) fireEvent.click(btn)

    await waitFor(() => expect(container.textContent).toContain('Task ID: t1'))

    // (b) What expands must be the PARSED body — never the raw "[System: …]"
    // content. `not.toContain('[System:')` runs with the card ALREADY expanded,
    // so wiring `toolResult={message.content}` (which would re-leak the raw
    // prefix inside the expanded <pre>) turns this red.
    expect(container.textContent).toContain('Subagent status update.')
    expect(container.textContent).not.toContain('[System:')
  })

  test('un mensaje de usuario normal sigue siendo burbuja plana', () => {
    const { value: ctx } = makeAuthCtx()

    const message = makeUserMessage({ content: 'hola mundo' })

    const { container } = renderBubble(message, ctx)

    expect(container.querySelector('[data-system-notice="true"]')).toBeNull()
    expect(container.textContent).toContain('hola mundo')
  })
})
