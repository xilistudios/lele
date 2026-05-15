import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import type { AgentFileInfo } from '../../lib/types'
import { SettingsHeader } from '../molecules'
import { Sidebar } from '../organisms/Sidebar'

type DirtyMap = Record<string, string | null> // fileName -> content if dirty, null if synced

export function AgentFilesPage() {
  const { agentId } = useParams<{ agentId: string }>()
  const { api } = useAuthContext()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()
  const navigate = useNavigate()

  const [files, setFiles] = useState<AgentFileInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeFile, setActiveFile] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [originalContent, setOriginalContent] = useState('')
  const [saving, setSaving] = useState(false)
  // useRef for dirty content cache — avoids fragile dependency on React batching semantics.
  // Refs are always up-to-date, so effects and callbacks always see the latest cache.
  const dirtyFilesRef = useRef<DirtyMap>({})

  // Load file list on mount
  // biome-ignore lint/correctness/useExhaustiveDependencies: activeFile is intentionally excluded to avoid re-triggering on mount
  useEffect(() => {
    if (!agentId) return
    ;(async () => {
      try {
        setLoading(true)
        setError(null)
        const res = await api.agentFiles(agentId)
        setFiles(res.files)
        if (res.files.length > 0 && !activeFile) {
          setActiveFile(res.files[0].name)
        }
      } catch (e) {
        setError((e as Error).message || 'Failed to load files')
      } finally {
        setLoading(false)
      }
    })()
  }, [agentId])

  // Load file content when active file changes
  // biome-ignore lint/correctness/useExhaustiveDependencies: api is stable, dirtyFilesRef is intentionally excluded
  useEffect(() => {
    if (!agentId || !activeFile) return

    // Check if we have dirty content cached from a previous edit
    const cached = dirtyFilesRef.current[activeFile]
    if (cached !== undefined && cached !== null) {
      // Need to also fetch original content for comparison
      ;(async () => {
        try {
          setError(null)
          const res = await api.agentFile(agentId, activeFile)
          setOriginalContent(res.content || '')
          // Restore dirty content from cache
          setContent(cached)
        } catch (e) {
          setError((e as Error).message || 'Failed to load file')
        }
      })()
      return
    }
    ;(async () => {
      try {
        setContent('')
        setError(null)
        const res = await api.agentFile(agentId, activeFile)
        setContent(res.content || '')
        setOriginalContent(res.content || '')
      } catch (e) {
        setError((e as Error).message || 'Failed to load file')
      }
    })()
  }, [agentId, activeFile])

  const isDirty = content !== originalContent

  const handleFileSelect = useCallback(
    (fileName: string) => {
      if (isDirty) {
        // Store current dirty content in ref before switching tabs
        dirtyFilesRef.current = {
          ...dirtyFilesRef.current,
          ...(activeFile && { [activeFile]: content !== originalContent ? content : null }),
        }
      }
      setActiveFile(fileName)
    },
    [activeFile, isDirty, content, originalContent],
  )

  const handleSave = async () => {
    if (!agentId || !activeFile || !isDirty) return
    try {
      setSaving(true)
      setError(null)
      await api.agentFileSave(agentId, activeFile, content)
      setOriginalContent(content)
      dirtyFilesRef.current = { ...dirtyFilesRef.current, [activeFile]: null }
    } catch (e) {
      setError((e as Error).message || 'Failed to save')
    } finally {
      setSaving(false)
    }
  }

  const handleDiscard = () => {
    setContent(originalContent)
    dirtyFilesRef.current = { ...dirtyFilesRef.current, ...(activeFile && { [activeFile]: null }) }
  }

  const hasAnyDirty =
    isDirty || Object.values(dirtyFilesRef.current).some((v) => v !== null && v !== undefined)

  const btnCls = 'rounded px-3 py-1.5 text-xs font-medium transition-colors disabled:opacity-50'
  const btnPrimary = `${btnCls} bg-cta-primary text-text-on-accent hover:bg-cta-hover`
  const btnSecondary = `${btnCls} bg-surface-secondary text-text-secondary hover:bg-surface-hover`
  const btnDanger = `${btnCls} bg-state-error-light text-state-error hover:bg-state-error/20`

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <SettingsHeader onToggleSidebar={onToggleSidebar} configPath={`Agent: ${agentId}`} />

        {loading ? (
          <div className="flex flex-1 items-center justify-center">
            <div className="text-sm text-text-secondary">Loading files...</div>
          </div>
        ) : error ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6">
            <p className="text-sm text-state-error">{error}</p>
            <button
              type="button"
              onClick={() => navigate('/settings/agents')}
              className={btnSecondary}
            >
              Back to Agents
            </button>
          </div>
        ) : (
          <div className="flex flex-1 overflow-hidden">
            {/* File Tabs Sidebar */}
            <div className="w-48 flex-shrink-0 border-r border-border-light bg-background-tertiary overflow-y-auto">
              <div className="p-3">
                <button
                  type="button"
                  onClick={() => navigate('/settings/agents')}
                  className="mb-3 text-xs text-text-tertiary hover:text-text-primary transition-colors flex items-center gap-1"
                >
                  <svg
                    width="12"
                    height="12"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                  >
                    <title>Back</title>
                    <polyline points="15 18 9 12 15 6" />
                  </svg>
                  Agents
                </button>
                <p className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-2">
                  Context Files
                </p>
              </div>
              {files.map((f) => {
                const cached = dirtyFilesRef.current[f.name]
                const fileDirty =
                  activeFile === f.name ? isDirty : cached !== null && cached !== undefined
                return (
                  <button
                    key={f.name}
                    type="button"
                    onClick={() => handleFileSelect(f.name)}
                    className={`w-full text-left px-3 py-2 text-xs transition-colors flex items-center justify-between ${
                      activeFile === f.name
                        ? 'bg-accent-subtle text-accent-primary border-l-2 border-accent-primary'
                        : 'text-text-secondary hover:bg-surface-hover border-l-2 border-transparent'
                    }`}
                  >
                    <span className="truncate">{f.name}</span>
                    {fileDirty && (
                      <span
                        className="ml-1 w-2 h-2 rounded-full bg-state-warning flex-shrink-0"
                        title="Modified"
                      />
                    )}
                  </button>
                )
              })}
            </div>

            {/* Editor Area */}
            <div className="flex flex-1 flex-col overflow-hidden">
              {/* Toolbar */}
              <div className="flex items-center justify-between px-4 py-2 border-b border-border-light bg-background-secondary">
                <div className="flex items-center gap-3">
                  <span className="text-sm font-medium text-text-primary">{activeFile}</span>
                  {isDirty && <span className="text-xs text-state-warning">Modified</span>}
                </div>
                <div className="flex items-center gap-2">
                  {isDirty && (
                    <button type="button" onClick={handleDiscard} className={btnDanger}>
                      Discard
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={handleSave}
                    disabled={!isDirty || saving}
                    className={btnPrimary}
                  >
                    {saving ? 'Saving...' : 'Save'}
                  </button>
                </div>
              </div>

              {/* Editor */}
              <div className="flex-1 overflow-hidden">
                <textarea
                  value={content}
                  onChange={(e) => setContent(e.target.value)}
                  className="w-full h-full resize-none bg-background-primary text-text-primary font-mono text-sm p-4 leading-relaxed outline-none border-0 focus:ring-0 placeholder-text-tertiary"
                  placeholder="File content..."
                  spellCheck={false}
                />
              </div>

              {/* Status Bar */}
              <div className="flex items-center justify-between px-4 py-1.5 border-t border-border-light bg-background-secondary text-xs text-text-tertiary">
                <span>{content.length.toLocaleString()} chars</span>
                <span>
                  {content.length === 0 ? 0 : content.split(/\n/).length.toLocaleString()} lines
                </span>
              </div>
            </div>
          </div>
        )}

        {/* Global save bar if any file is dirty */}
        {hasAnyDirty && !loading && (
          <div className="flex items-center justify-between px-4 py-2 border-t border-state-warning/30 bg-state-warning-light">
            <span className="text-xs text-state-warning">You have unsaved changes</span>
            <button
              type="button"
              onClick={handleSave}
              disabled={!isDirty || saving}
              className={btnPrimary}
            >
              {saving ? 'Saving...' : 'Save Current File'}
            </button>
          </div>
        )}
      </main>
    </div>
  )
}
