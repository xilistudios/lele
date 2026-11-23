import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { type RenderResult, fireEvent, render, waitFor } from '@testing-library/react'
import { type ReactElement, useState } from 'react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { Attachment } from '../../lib/types'
import i18n from '../../test/i18n'
import { clearAuthedImageCache } from './AuthedImage'
import { ImageLightbox } from './ImageLightbox'

const originalCreateObjectURL = URL.createObjectURL
const originalRevokeObjectURL = URL.revokeObjectURL
const originalIntersectionObserver = globalThis.IntersectionObserver

function makeAuthCtx() {
  const fileBlob = mock(async () => new Blob(['fake-png'], { type: 'image/png' }))
  return {
    api: { fileBlob },
    apiUrl: 'http://127.0.0.1:18793',
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => ({ token: '', refresh_token: '', device_name: '', client_id: '' }),
    ensureSession: async () => null,
    isLoading: false,
  } as unknown as AuthContextValue
}

function renderWithAuth(ui: ReactElement) {
  return render(<AuthContext.Provider value={makeAuthCtx()}>{ui}</AuthContext.Provider>)
}

async function mountLightbox(ui: ReactElement): Promise<RenderResult> {
  const result = renderWithAuth(ui)
  await waitFor(() => expect(document.querySelector('img')).not.toBeNull())
  return result
}

const image = (path: string): Attachment => ({
  path,
  name: path.split('/').pop() ?? 'photo.png',
  mime_type: 'image/png',
  kind: 'image',
})

const TWO = [image('/att/a.png'), image('/att/b.png')]

function counter(): string {
  return document.querySelector('[data-testid="image-lightbox-counter"]')?.textContent ?? ''
}

beforeEach(() => {
  clearAuthedImageCache()
  // @ts-expect-error removing for test
  globalThis.IntersectionObserver = undefined
  URL.createObjectURL = mock(
    (_blob: Blob) => 'blob:test-url/lightbox',
  ) as typeof URL.createObjectURL
  URL.revokeObjectURL = mock(() => {}) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
  globalThis.IntersectionObserver = originalIntersectionObserver
  document.body.style.overflow = ''
})

describe('ImageLightbox', () => {
  test('renders into document.body, outside the React tree container', async () => {
    const { container } = await mountLightbox(
      <ImageLightbox images={TWO} index={0} onClose={() => {}} />,
    )

    expect(container.querySelector('[data-testid="image-lightbox"]')).toBeNull()
    expect(document.body.querySelector('[data-testid="image-lightbox"]')).not.toBeNull()
  })

  test('exposes a modal dialog labelled with the view-image message', async () => {
    const { getByRole } = await mountLightbox(
      <ImageLightbox images={TWO} index={0} onClose={() => {}} />,
    )

    const dialog = getByRole('dialog', { name: i18n.t('chat.viewImage') })
    expect(dialog.tagName).toBe('DIALOG')
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(dialog.getAttribute('aria-label')).toBe(i18n.t('chat.viewImage'))
  })

  test('opens itself through showModal when the platform provides it', async () => {
    const dialogProto = HTMLDialogElement.prototype as { showModal?: () => void }
    const original = dialogProto.showModal
    const showModal = mock(() => {})
    dialogProto.showModal = showModal

    try {
      const { unmount } = await mountLightbox(
        <ImageLightbox images={TWO} index={0} onClose={() => {}} />,
      )

      expect(showModal).toHaveBeenCalledTimes(1)
      const dialog = document.querySelector('[data-testid="image-lightbox"]')
      expect(dialog?.tagName).toBe('DIALOG')
      expect(document.activeElement).toBe(
        document.querySelector(`button[aria-label="${i18n.t('common.close')}"]`),
      )
      unmount()
    } finally {
      if (original) dialogProto.showModal = original
      else dialogProto.showModal = undefined
    }
  })

  test('the native cancel event is prevented and routed to onClose', async () => {
    const onClose = mock(() => {})
    await mountLightbox(<ImageLightbox images={TWO} index={0} onClose={onClose} />)

    const dialog = document.querySelector('[data-testid="image-lightbox"]')
    if (!dialog) throw new Error('lightbox not rendered')
    const event = new window.Event('cancel', { cancelable: true })
    dialog.dispatchEvent(event)

    expect(event.defaultPrevented).toBe(true)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('Escape closes the lightbox', async () => {
    const onClose = mock(() => {})
    await mountLightbox(<ImageLightbox images={TWO} index={0} onClose={onClose} />)

    fireEvent.keyDown(document, { key: 'Escape' })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('ArrowRight advances the index and wraps around at the end', async () => {
    const onIndexChange = mock(() => {})
    const { rerender } = await mountLightbox(
      <ImageLightbox images={TWO} index={0} onClose={() => {}} onIndexChange={onIndexChange} />,
    )

    fireEvent.keyDown(document, { key: 'ArrowRight' })
    expect(onIndexChange).toHaveBeenLastCalledWith(1)

    rerender(
      <AuthContext.Provider value={makeAuthCtx()}>
        <ImageLightbox images={TWO} index={1} onClose={() => {}} onIndexChange={onIndexChange} />
      </AuthContext.Provider>,
    )
    fireEvent.keyDown(document, { key: 'ArrowRight' })
    expect(onIndexChange).toHaveBeenLastCalledWith(0)
  })

  test('ArrowLeft steps back and wraps to the last image', async () => {
    const onIndexChange = mock(() => {})
    await mountLightbox(
      <ImageLightbox images={TWO} index={0} onClose={() => {}} onIndexChange={onIndexChange} />,
    )

    fireEvent.keyDown(document, { key: 'ArrowLeft' })

    expect(onIndexChange).toHaveBeenLastCalledWith(1)
  })

  test('a click on the backdrop closes the lightbox', async () => {
    const onClose = mock(() => {})
    await mountLightbox(<ImageLightbox images={TWO} index={0} onClose={onClose} />)

    const dialog = document.querySelector('[data-testid="image-lightbox"]')
    if (!dialog) throw new Error('lightbox not rendered')
    fireEvent.click(dialog)

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('a click on the image does not close the lightbox', async () => {
    const onClose = mock(() => {})
    await mountLightbox(<ImageLightbox images={TWO} index={0} onClose={onClose} />)

    const img = document.querySelector('img')
    if (!img) throw new Error('image not rendered')
    fireEvent.click(img)

    expect(onClose).not.toHaveBeenCalled()
  })

  test('a single image renders no previous/next controls and no counter', async () => {
    await mountLightbox(
      <ImageLightbox images={[image('/att/only.png')]} index={0} onClose={() => {}} />,
    )

    expect(
      document.querySelector(`button[aria-label="${i18n.t('chat.previousImage')}"]`),
    ).toBeNull()
    expect(document.querySelector(`button[aria-label="${i18n.t('chat.nextImage')}"]`)).toBeNull()
    expect(document.querySelector('[data-testid="image-lightbox-counter"]')).toBeNull()
  })

  test('several images render the controls and a 1-based counter', async () => {
    await mountLightbox(<ImageLightbox images={TWO} index={1} onClose={() => {}} />)

    expect(
      document.querySelector(`button[aria-label="${i18n.t('chat.previousImage')}"]`),
    ).not.toBeNull()
    expect(
      document.querySelector(`button[aria-label="${i18n.t('chat.nextImage')}"]`),
    ).not.toBeNull()
    expect(counter()).toBe('2 / 2')
  })

  test('the nav buttons move the index when clicked', async () => {
    const onIndexChange = mock(() => {})
    await mountLightbox(
      <ImageLightbox images={TWO} index={0} onClose={() => {}} onIndexChange={onIndexChange} />,
    )

    const next = document.querySelector(`button[aria-label="${i18n.t('chat.nextImage')}"]`)
    if (!next) throw new Error('next button missing')
    fireEvent.click(next)

    expect(onIndexChange).toHaveBeenLastCalledWith(1)
  })

  test('focuses the close button on mount and locks body scroll', async () => {
    await mountLightbox(<ImageLightbox images={TWO} index={0} onClose={() => {}} />)

    const close = document.querySelector(`button[aria-label="${i18n.t('common.close')}"]`)
    expect(document.activeElement).toBe(close)
    expect(document.body.style.overflow).toBe('hidden')
  })

  test('restores body scroll and focus on unmount', async () => {
    const trigger = document.createElement('button')
    document.body.appendChild(trigger)
    trigger.focus()

    const { unmount } = await mountLightbox(
      <ImageLightbox images={TWO} index={0} onClose={() => {}} />,
    )
    expect(document.body.style.overflow).toBe('hidden')

    unmount()

    expect(document.body.style.overflow).toBe('')
    expect(document.activeElement).toBe(trigger)
    trigger.remove()
  })

  test('navigating keeps the counter in sync with a stateful parent', async () => {
    function Harness() {
      const [index, setIndex] = useState(0)
      return (
        <ImageLightbox images={TWO} index={index} onClose={() => {}} onIndexChange={setIndex} />
      )
    }

    await mountLightbox(<Harness />)
    expect(counter()).toBe('1 / 2')

    fireEvent.keyDown(document, { key: 'ArrowRight' })
    expect(counter()).toBe('2 / 2')

    fireEvent.keyDown(document, { key: 'ArrowRight' })
    expect(counter()).toBe('1 / 2')
  })
})
