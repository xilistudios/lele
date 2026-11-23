import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { Attachment } from '../../lib/types'
import i18n from '../../test/i18n'
import { clearAuthedImageCache } from './AuthedImage'
import { ImageMosaic } from './ImageMosaic'

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

function renderWithAuth(ui: React.ReactElement) {
  return render(<AuthContext.Provider value={makeAuthCtx()}>{ui}</AuthContext.Provider>)
}

const image = (name: string): Attachment => ({
  path: `/att/${name}`,
  name,
  mime_type: 'image/png',
  kind: 'image',
})

const IMAGES = [image('one.png'), image('two.png'), image('three.png'), image('four.png')]

function tiles(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>('[data-testid="image-mosaic-tile"]'))
}

beforeEach(() => {
  clearAuthedImageCache()
  // @ts-expect-error removing for test
  globalThis.IntersectionObserver = undefined
  URL.createObjectURL = mock((_blob: Blob) => 'blob:test-url/mosaic') as typeof URL.createObjectURL
  URL.revokeObjectURL = mock(() => {}) as typeof URL.revokeObjectURL
})

afterEach(() => {
  URL.createObjectURL = originalCreateObjectURL
  URL.revokeObjectURL = originalRevokeObjectURL
  globalThis.IntersectionObserver = originalIntersectionObserver
  document.body.style.overflow = ''
})

describe('ImageMosaic', () => {
  test('renders exactly one tile for a single image', async () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={[IMAGES[0]]} />)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.getAttribute('data-count')).toBe('1')
    expect(tiles(container).length).toBe(1)
    await waitFor(() => expect(container.querySelector('img')).not.toBeNull())
  })

  test('uses two columns for two images', () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={IMAGES.slice(0, 2)} />)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.className).toContain('grid-cols-2')
    expect(mosaic?.className).not.toContain('sm:grid-cols-3')
    expect(tiles(container).length).toBe(2)
  })

  test('renders four tiles and switches to three columns on sm for 3+ images', () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={IMAGES} />)

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.getAttribute('data-count')).toBe('4')
    expect(mosaic?.className).toContain('sm:grid-cols-3')
    expect(tiles(container).length).toBe(4)
  })

  test('tiles are square buttons labelled with the view-image message', () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={IMAGES.slice(0, 2)} />)

    for (const tile of tiles(container)) {
      expect(tile.tagName).toBe('BUTTON')
      expect(tile.getAttribute('aria-label')).toBe(i18n.t('chat.viewImage'))
      expect(tile.className).toContain('aspect-square')
    }
  })

  test('a single image tile is not square and uses object-contain', async () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={[IMAGES[0]]} />)

    const [tile] = tiles(container)
    expect(tile.className).not.toContain('aspect-square')
    expect(tile.className).toContain('max-h-96')

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img?.className).toContain('object-contain')
    })
  })

  test('multiple images use object-cover with the hover zoom', async () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={IMAGES.slice(0, 2)} />)

    await waitFor(() => {
      const img = container.querySelector('img')
      expect(img?.className).toContain('object-cover')
      expect(img?.className).toContain('group-hover:scale-[1.02]')
    })
  })

  test('clicking tile N opens the lightbox at index N', async () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={IMAGES} />)

    fireEvent.click(tiles(container)[2])

    await waitFor(() => {
      const lightbox = document.querySelector('[data-testid="image-lightbox"]')
      expect(lightbox).not.toBeNull()
      expect(lightbox?.querySelector('[data-testid="image-lightbox-counter"]')?.textContent).toBe(
        '3 / 4',
      )
    })
  })

  test('the lightbox closes back to no dialog', async () => {
    const { container } = renderWithAuth(<ImageMosaic attachments={IMAGES.slice(0, 2)} />)

    fireEvent.click(tiles(container)[0])
    await waitFor(() =>
      expect(document.querySelector('[data-testid="image-lightbox"]')).not.toBeNull(),
    )

    fireEvent.keyDown(document, { key: 'Escape' })

    await waitFor(() => expect(document.querySelector('[data-testid="image-lightbox"]')).toBeNull())
  })

  test('non-image attachments are filtered out of the mosaic', () => {
    const { container } = renderWithAuth(
      <ImageMosaic
        attachments={[IMAGES[0], { path: '/tmp/report.pdf', name: 'report.pdf', kind: 'file' }]}
      />,
    )

    const mosaic = container.querySelector('[data-testid="image-mosaic"]')
    expect(mosaic?.getAttribute('data-count')).toBe('1')
    expect(container.textContent ?? '').not.toContain('report.pdf')
  })

  test('renders nothing when there is no image attachment', () => {
    const { container } = renderWithAuth(
      <ImageMosaic attachments={[{ path: '/tmp/report.pdf', name: 'report.pdf' }]} />,
    )

    expect(container.querySelector('[data-testid="image-mosaic"]')).toBeNull()
  })

  test('renderTileOverlay renders once per tile with its attachment', () => {
    const overlay = mock((attachment: Attachment, index: number) => (
      <span data-testid={`overlay-${index}`}>{attachment.name}</span>
    ))

    const { container } = renderWithAuth(
      <ImageMosaic attachments={IMAGES.slice(0, 2)} renderTileOverlay={overlay} />,
    )

    expect(overlay).toHaveBeenCalledTimes(2)
    expect(container.querySelectorAll('[data-testid^="overlay-"]').length).toBe(2)
  })
})
