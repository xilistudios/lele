import './test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { createElement } from 'react'
import './test/i18n'
import { FolderPickerModal } from './components/organisms/FolderPickerModal'
import { AuthProvider } from './contexts/AuthContext'

const originalFetch = globalThis.fetch

type FsListPayload = {
  path: string
  parent: string
  entries: { name: string; path: string; is_dir: boolean }[]
  home: string
  roots: string[]
  truncated: boolean
}

function fsResponse(payload: FsListPayload) {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function wrapper({ children }: { children: ReactNode }) {
  return createElement(
    AuthProvider,
    { defaultApiUrl: 'http://127.0.0.1:18793', children },
    children,
  )
}

describe('FolderPickerModal', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem(
      'lele.session',
      JSON.stringify({ token: 'token', refresh_token: 'refresh' }),
    )
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    localStorage.clear()
    cleanup()
  })

  test('renders entries from fs/list and selects a folder on confirm', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/fs/list')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      if (url.includes('path=%2Fworkspace')) {
        return fsResponse({
          path: '/workspace',
          parent: '',
          entries: [{ name: 'src', path: '/workspace/src', is_dir: true }],
          home: '/home/alfredo',
          roots: ['/home/alfredo', '/workspace'],
          truncated: false,
        })
      }
      return fsResponse({
        path: '/home/alfredo',
        parent: '/',
        entries: [
          { name: 'projects', path: '/home/alfredo/projects', is_dir: true },
          { name: 'docs', path: '/home/alfredo/docs', is_dir: true },
        ],
        home: '/home/alfredo',
        roots: ['/home/alfredo'],
        truncated: false,
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const onSelect = mock(() => undefined)
    const { getByText, getByRole } = render(
      createElement(FolderPickerModal, {
        open: true,
        onClose: () => undefined,
        onSelect,
        currentFolder: '/home/alfredo',
      }),
      { wrapper },
    )

    // Entries fetched from the service are rendered
    await waitFor(() => {
      expect(getByText('projects')).not.toBeNull()
    })
    expect(getByText('docs')).not.toBeNull()

    // Confirm button selects the directory currently being listed
    fireEvent.click(getByRole('button', { name: /Seleccionar esta carpeta/i }))
    expect(onSelect).toHaveBeenCalledWith('/home/alfredo')
  })

  test('clicking an entry navigates and double-click selects it', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/fs/list')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      if (url.includes('path=%2Fhome%2Falfredo%2Fprojects')) {
        return fsResponse({
          path: '/home/alfredo/projects',
          parent: '/home/alfredo',
          entries: [],
          home: '/home/alfredo',
          roots: ['/home/alfredo'],
          truncated: false,
        })
      }
      return fsResponse({
        path: '/home/alfredo',
        parent: '/',
        entries: [{ name: 'projects', path: '/home/alfredo/projects', is_dir: true }],
        home: '/home/alfredo',
        roots: ['/home/alfredo'],
        truncated: false,
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const onSelect = mock(() => undefined)
    const { getByText } = render(
      createElement(FolderPickerModal, {
        open: true,
        onClose: () => undefined,
        onSelect,
      }),
      { wrapper },
    )

    await waitFor(() => {
      expect(getByText('projects')).not.toBeNull()
    })

    // Double-click selects the entry itself as the target folder
    fireEvent.doubleClick(getByText('projects'))
    expect(onSelect).toHaveBeenCalledWith('/home/alfredo/projects')
  })
})
