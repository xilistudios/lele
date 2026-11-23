import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { toChatMessages } from '../../lib/chatMessageBuilder'
import type { ChatMessage } from '../../lib/types'
import i18n from '../../test/i18n'
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
      expect(img?.getAttribute('src')).toBe('blob:test-url/msg')
      expect(img?.getAttribute('src')).not.toContain('/api/v1/files/view?')
    })

    expect(fileBlob).toHaveBeenCalledWith(
      '/home/x/.lele/workspace-demo/attachments/20260919/aaaa1111_bbbb2222_foto.png',
    )
  })

  test('assistant download button calls fileBlob and triggers download', async () => {
    const downloadBlob = new Blob(['download-data'], { type: 'application/octet-stream' })
    const fileBlob = mock(async () => downloadBlob)
    const { value: ctx } = makeAuthCtx({ fileBlob })

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

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img).toBeTruthy()
    })

    const downloadBtn = container.querySelector(`button[aria-label^="${i18n.t('chat.download')}"]`)
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
    expect(container.textContent).not.toContain('[System: subagent]')
    expect(container.textContent).not.toContain('Task ID: t1')

    const btn = container.querySelector('button[aria-expanded]')
    expect(btn).toBeTruthy()

    expect(btn?.textContent ?? '').toContain('subagent')

    if (btn) fireEvent.click(btn)

    await waitFor(() => expect(container.textContent).toContain('Task ID: t1'))

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

const IMG_A = '/home/x/.lele/workspace-demo/attachments/20261123/aaaa1111_one.png'
const IMG_B = '/home/x/.lele/workspace-demo/attachments/20261123/bbbb2222_two.png'

const imageAttachment = (path: string) => ({
  path,
  name: path.split('/').pop() ?? 'image.png',
  mime_type: 'image/png',
  kind: 'image',
})

describe('MessageBubble — image mosaic', () => {
  test('a user message with image attachments renders the mosaic instead of a bare image row', async () => {
    const { value: ctx } = makeAuthCtx()
    const message = makeUserMessage({
      content: 'mira esto',
      attachments: [imageAttachment(IMG_A), imageAttachment(IMG_B)],
    })

    const { container } = renderBubble(message, ctx)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic).toBeTruthy()
    expect(mosaic?.getAttribute('data-count')).toBe('2')
    expect(mosaic?.querySelectorAll('[data-testid="image-mosaic-tile"]').length).toBe(2)

    await waitFor(() => {
      const img = mosaic?.querySelector('img')
      expect(img).toBeTruthy()
      expect(img?.getAttribute('alt')).toBe('aaaa1111_one.png')
    })
  })

  test('a single image keeps the mosaic contract with one tile', async () => {
    const { value: ctx } = makeAuthCtx()
    const message = makeUserMessage({ attachments: [imageAttachment(IMG_A)] })

    const { container } = renderBubble(message, ctx)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.getAttribute('data-count')).toBe('1')
    expect(mosaic?.querySelectorAll('[data-testid="image-mosaic-tile"]').length).toBe(1)
    await waitFor(() => expect(mosaic?.querySelector('img')).toBeTruthy())
  })

  test('non-image attachments stay as chips, outside the mosaic', () => {
    const { value: ctx } = makeAuthCtx()
    const message = makeUserMessage({
      attachments: [imageAttachment(IMG_A), { path: '/tmp/report.pdf', name: 'report.pdf' }],
    })

    const { container } = renderBubble(message, ctx)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.getAttribute('data-count')).toBe('1')
    expect(mosaic?.textContent ?? '').not.toContain('report.pdf')
    expect(container.textContent).toContain('report.pdf')
  })

  test('an assistant message with image attachments renders the mosaic plus a per-image download', async () => {
    const { value: ctx } = makeAuthCtx()
    const message = makeAssistantMessage({
      content: 'aqui esta el grafico',
      attachments: [imageAttachment(IMG_A), imageAttachment(IMG_B)],
    })

    const { container } = renderBubble(message, ctx)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.getAttribute('data-count')).toBe('2')
    expect(container.textContent).toContain('aqui esta el grafico')

    const downloadButtons = mosaic?.querySelectorAll(
      `button[aria-label^="${i18n.t('chat.download')}"]`,
    )
    expect(downloadButtons?.length).toBe(2)

    await waitFor(() => expect(mosaic?.querySelector('img')).toBeTruthy())
  })

  test('an assistant message with only non-image attachments keeps the file cards', () => {
    const { value: ctx } = makeAuthCtx()
    const message = makeAssistantMessage({
      content: 'report attached',
      attachments: [{ path: '/tmp/report.pdf', name: 'report.pdf', caption: 'Q3 numbers' }],
    })

    const { container } = renderBubble(message, ctx)

    expect(container.querySelector('[data-testid="image-mosaic"]')).toBeNull()
    expect(container.textContent).toContain('report.pdf')
    expect(container.textContent).toContain('Q3 numbers')
  })

  test('a persisted file: history row renders as an assistant bubble with the mosaic', async () => {
    const { value: ctx } = makeAuthCtx()
    const [message] = toChatMessages(
      [
        {
          id: 'h1',
          role: 'tool',
          content: '',
          tool_call_id: 'file:ab12cd34ef56ab78',
          attachments: [imageAttachment(IMG_A)],
        },
      ],
      'session-1',
    )

    expect(message.role).toBe('assistant')

    const { container } = renderBubble(message, ctx)

    const bubble = container.querySelector('[data-role="assistant"]')
    expect(bubble).toBeTruthy()
    expect(bubble?.querySelector('[data-testid="image-mosaic"]')?.getAttribute('data-count')).toBe(
      '1',
    )
    await waitFor(() => expect(bubble?.querySelector('img')).toBeTruthy())
  })
})
