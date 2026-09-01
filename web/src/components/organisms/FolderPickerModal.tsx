import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthContext } from '../../contexts/AuthContext'
import type { FsListEntry, FsListResponse } from '../../lib/types'
import { Button, Modal, Spinner } from '../atoms'
import { IconButton } from '../atoms/IconButton'
import { ChevronLeftIcon, FolderIcon, HomeIcon } from '../atoms/Icons'

type Props = {
  open: boolean
  onClose: () => void
  onSelect: (path: string) => void
  currentFolder?: string
}

/**
 * Server-side folder browser used by the composer "+" button.
 *
 * Navigates GET /api/v1/fs/list (one directory per request), keeps a local
 * name filter, exposes clickable breadcrumbs + a "go up" button, quick-jump
 * chips for the sandbox roots (Home, /tmp, cwd), and a footer button that
 * confirms the directory currently being listed. Double-click on an entry
 * selects it directly as the target folder.
 */
export function FolderPickerModal({ open, onClose, onSelect, currentFolder }: Props) {
  const { t } = useTranslation()
  const { api } = useAuthContext()

  const [listing, setListing] = useState<FsListResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)

  // Monotonic request id: only the latest navigation may update state, so a
  // slow response cannot overwrite a newer directory (out-of-order responses).
  const requestIdRef = useRef(0)

  const loadPath = useCallback(
    async (path: string) => {
      const id = ++requestIdRef.current
      setLoading(true)
      setError(null)
      try {
        const res = await api.fsList(path)
        if (requestIdRef.current !== id) return
        setListing(res)
      } catch (err) {
        if (requestIdRef.current !== id) return
        setError((err as Error).message || t('chat.folderLoadError', 'Failed to load folder'))
        setListing(null)
      } finally {
        if (requestIdRef.current === id) setLoading(false)
      }
    },
    [api, t],
  )

  // On open, load the current folder (if any) so the picker starts where the
  // session left off; otherwise the backend defaults to the user home.
  useEffect(() => {
    if (!open) return
    setQuery('')
    loadPath(currentFolder ?? '')
    searchRef.current?.focus()
  }, [open, loadPath, currentFolder])

  const goUp = () => {
    if (!listing?.parent) return
    loadPath(listing.parent)
  }

  const openEntry = (entry: FsListEntry) => {
    loadPath(entry.path)
  }

  // Breadcrumb: split the listed path into clickable prefixes.
  const segments = useMemo(() => {
    const path = listing?.path ?? ''
    if (!path) return [] as { label: string; path: string }[]
    const parts = path.split('/').filter(Boolean)
    return parts.map((label, index) => ({
      label,
      path: `/${parts.slice(0, index + 1).join('/')}`,
    }))
  }, [listing?.path])

  const filteredEntries = useMemo(() => {
    const entries = listing?.entries ?? []
    const q = query.trim().toLowerCase()
    if (!q) return entries
    return entries.filter((entry) => entry.name.toLowerCase().includes(q))
  }, [listing?.entries, query])

  const roots = listing?.roots ?? []
  const home = listing?.home ?? ''

  const confirmCurrent = () => {
    if (!listing?.path) return
    onSelect(listing.path)
  }

  return (
    <Modal
      isOpen={open}
      onClose={onClose}
      title={t('chat.selectFolder', 'Select folder')}
      size="md"
    >
      <div className="flex flex-col">
        {/* Toolbar: up button + search */}
        <div className="flex items-center gap-2 px-6 pt-4 pb-2">
          <IconButton
            onClick={goUp}
            disabled={!listing?.parent || loading}
            title={t('chat.up', 'Go up')}
            ariaLabel={t('chat.up', 'Go up')}
          >
            <ChevronLeftIcon size={16} />
          </IconButton>
          <input
            ref={searchRef}
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('chat.searchFolders', 'Search folders...')}
            aria-label={t('chat.searchFolders', 'Search folders...')}
            className="flex-1 rounded-lg border border-border bg-background-secondary px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-2 focus:ring-accent-primary/30 focus:border-accent-primary/50"
          />
        </div>

        {/* Breadcrumb of the current path */}
        {segments.length > 0 && (
          <nav
            aria-label={t('chat.currentFolder', 'Current folder')}
            className="flex flex-wrap items-center gap-1 px-6 pb-2 text-xs text-text-tertiary"
          >
            {segments.map((segment, index) => (
              <span key={segment.path} className="flex items-center gap-1 min-w-0">
                {index > 0 && <span aria-hidden="true">/</span>}
                <button
                  type="button"
                  onClick={() => loadPath(segment.path)}
                  className="max-w-[10rem] truncate rounded px-1 py-0.5 text-left text-text-secondary hover:text-text-primary hover:bg-background-tertiary transition-colors"
                  title={segment.path}
                >
                  {segment.label}
                </button>
              </span>
            ))}
          </nav>
        )}

        {/* Roots quick-jump chips */}
        {roots.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-6 pb-3">
            {roots.map((root) => {
              const isHome = home !== '' && root === home
              const label = isHome
                ? t('chat.folderHome', 'Home')
                : root === '/'
                  ? '/'
                  : root.split('/').filter(Boolean).pop() || root
              return (
                <button
                  key={root}
                  type="button"
                  onClick={() => loadPath(root)}
                  title={root}
                  className={`flex items-center gap-1 rounded-full border px-2.5 py-1 text-[11px] transition-colors ${
                    listing?.path === root
                      ? 'border-accent-primary/50 bg-accent-primary/10 text-accent-primary'
                      : 'border-border bg-background-secondary text-text-secondary hover:text-text-primary hover:border-border-light'
                  }`}
                >
                  {isHome ? <HomeIcon size={12} /> : <FolderIcon size={12} />}
                  <span className="max-w-[10rem] truncate">{label}</span>
                </button>
              )
            })}
          </div>
        )}

        {/* Entries list */}
        <div className="max-h-72 min-h-40 overflow-y-auto px-6 pb-2">
          {loading ? (
            <div className="flex items-center justify-center py-12">
              <Spinner />
              <span className="ml-3 text-sm text-text-secondary">{t('common.loading')}</span>
            </div>
          ) : error ? (
            <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
              <p className="text-sm text-state-error">{error}</p>
              <Button
                variant="secondary"
                size="sm"
                type="button"
                onClick={() => loadPath(listing?.path ?? currentFolder ?? '')}
              >
                {t('chat.folderRetry', 'Retry')}
              </Button>
            </div>
          ) : filteredEntries.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12 text-center">
              <p className="text-sm text-text-secondary">
                {query.trim()
                  ? t('chat.noFoldersFound', 'No folders match your search')
                  : t('chat.noFolders', 'No folders here')}
              </p>
            </div>
          ) : (
            <ul className="space-y-1 pt-1">
              {filteredEntries.map((entry) => (
                <li key={entry.path}>
                  <button
                    type="button"
                    onClick={() => openEntry(entry)}
                    onDoubleClick={() => onSelect(entry.path)}
                    title={entry.path}
                    className="flex w-full items-center gap-2 rounded-lg border border-transparent px-3 py-2 text-left text-sm text-text-primary transition-colors hover:border-border hover:bg-background-secondary"
                  >
                    <FolderIcon size={16} className="flex-shrink-0 text-accent-primary" />
                    <span className="truncate">{entry.name}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {!loading && !error && listing?.truncated && (
            <p className="px-1 py-2 text-[11px] text-text-tertiary">
              {t('chat.folderTruncated', 'Too many folders — showing the first ones only')}
            </p>
          )}
        </div>

        {/* Footer: confirm the directory being listed */}
        <div className="flex items-center justify-between gap-3 border-t border-border px-6 py-4">
          <span
            className="min-w-0 flex-1 truncate text-xs text-text-tertiary"
            title={listing?.path}
          >
            {listing?.path ?? ''}
          </span>
          <div className="flex flex-shrink-0 items-center gap-2">
            <Button variant="ghost" size="sm" type="button" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="primary"
              size="sm"
              type="button"
              disabled={!listing?.path || loading || error != null}
              onClick={confirmCurrent}
            >
              {t('chat.confirmFolder', 'Select this folder')}
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}
