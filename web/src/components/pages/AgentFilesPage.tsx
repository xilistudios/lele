import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAuthContext } from '../../contexts/AuthContext'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { SettingsHeader } from '../molecules'
import { Sidebar } from '../organisms/Sidebar'
import type { AgentFileInfo } from '../../lib/types'

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
  const [dirtyFiles, setDirtyFiles] = useState<DirtyMap>({})

  // Load file list on mount
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
  }, [agentId]) // eslint-disable-line react-hooks/exhaustive-deps

  // Load file content when active file changes
  useEffect(() => {
    if (!agentId || !activeFile) return

    // Check if we have dirty content cached from a previous edit
    if (dirtyFiles[activeFile] !== undefined && dirtyFiles[activeFile] !== null) {
      // Need to also fetch original content for comparison
      ;(async () => {
        try {
          setError(null)
          const res = await api.agentFile(agentId, activeFile)
          setOriginalContent(res.content || '')
          // Restore dirty content
          setContent(dirtyFiles[activeFile]!)
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
    // dirtyFiles is intentionally NOT in deps: it's only used as a cache
    // when restoring dirty content on tab switch. handleFileSelect updates
    // dirtyFiles synchronously before setActiveFile, and React 18+ batching
    // ensures the effect sees the fresh dirtyFiles value when activeFile changes.
  }, [agentId, activeFile]) // eslint-disable-line react-hooks/exhaustive-deps

  const isDirty = content !== originalContent

  const handleFileSelect = useCallback(
    (fileName: string) => {
      if (isDirty) {
        // Store current dirty content before switching
        setDirtyFiles((prev) => ({
          ...prev,
          [activeFile!]: content !== originalContent ? content : null,
        }))
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
      setDirtyFiles((prev) => ({ ...prev, [activeFile]: null }))
    } catch (e) {
      setError((e as Error).message || 'Failed to save')
    } finally {
      setSaving(false)
    }
  }

  const handleDiscard = () => {
    setContent(originalContent)
    setDirtyFiles((prev) => ({ ...prev, [activeFile!]: null }))
  }

  const hasAnyDirty =
    isDirty || Object.values(dirtyFiles).some((v) => v !== null && v !== undefined)

  const btnCls =
    'rounded px-3 py-1.5 text-xs font-medium transition-colors disabled:opacity-50'
  const btnPrimary = `${btnCls} bg-blue-600 text-white hover:bg-blue-500`
  const btnSecondary = `${btnCls} bg-[#2a2a2a] text-[#888] hover:bg-[#333]`
  const btnDanger = `${btnCls} bg-red-800/30 text-red-400 hover:bg-red-800/50`

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <SettingsHeader
          onToggleSidebar={onToggleSidebar}
          onLogout={() => {}}
          configPath={`Agent: ${agentId}`}
        />

        {loading ? (
          <div className="flex flex-1 items-center justify-center">
            <div className="text-sm text-text-secondary">Loading files...</div>
          </div>
        ) : error ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6">
            <p className="text-sm text-red-400">{error}</p>
            <button type="button" onClick={() => navigate('/settings/agents')} className={btnSecondary}>
              Back to Agents
            </button>
          </div>
        ) : (
          <div className="flex flex-1 overflow-hidden">
            {/* File Tabs Sidebar */}
            <div className="w-48 flex-shrink-0 border-r border-border-light bg-[#111] overflow-y-auto">
              <div className="p-3">
                <button
                  type="button"
                  onClick={() => navigate('/settings/agents')}
                  className="mb-3 text-xs text-[#666] hover:text-[#aaa] transition-colors flex items-center gap-1"
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                    <title>Back</title>
                    <polyline points="15 18 9 12 15 6" />
                  </svg>
                  Agents
                </button>
                <p className="text-xs font-medium text-[#666] uppercase tracking-wider mb-2">
                  Context Files
                </p>
              </div>
              {files.map((f) => {
                const fileDirty =
                  activeFile === f.name
                    ? isDirty
                    : dirtyFiles[f.name] !== null && dirtyFiles[f.name] !== undefined
                return (
                  <button
                    key={f.name}
                    type="button"
                    onClick={() => handleFileSelect(f.name)}
                    className={`w-full text-left px-3 py-2 text-xs transition-colors flex items-center justify-between ${
                      activeFile === f.name
                        ? 'bg-blue-600/20 text-blue-400 border-l-2 border-blue-500'
                        : 'text-[#aaa] hover:bg-[#1a1a1a] border-l-2 border-transparent'
                    }`}
                  >
                    <span className="truncate">{f.name}</span>
                    {fileDirty && (
                      <span className="ml-1 w-2 h-2 rounded-full bg-amber-400 flex-shrink-0" title="Modified" />
                    )}
                  </button>
                )
              })}
            </div>

            {/* Editor Area */}
            <div className="flex flex-1 flex-col overflow-hidden">
              {/* Toolbar */}
              <div className="flex items-center justify-between px-4 py-2 border-b border-border-light bg-[#0d0d0d]">
                <div className="flex items-center gap-3">
                  <span className="text-sm font-medium text-[#e0e0e0]">{activeFile}</span>
                  {isDirty && (
                    <span className="text-xs text-amber-400">Modified</span>
                  )}
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
                  className="w-full h-full resize-none bg-[#0a0a0a] text-[#e0e0e0] font-mono text-sm p-4 leading-relaxed outline-none border-0 focus:ring-0 placeholder-[#333]"
                  placeholder="File content..."
                  spellCheck={false}
                />
              </div>

              {/* Status Bar */}
              <div className="flex items-center justify-between px-4 py-1.5 border-t border-border-light bg-[#0d0d0d] text-xs text-[#555]">
                <span>{content.length.toLocaleString()} chars</span>
                <span>{content.length === 0 ? 0 : content.split(/\n/).length.toLocaleString()} lines</span>
              </div>
            </div>
          </div>
        )}

        {/* Global save bar if any file is dirty */}
        {hasAnyDirty && !loading && (
          <div className="flex items-center justify-between px-4 py-2 border-t border-amber-800/30 bg-amber-900/10">
            <span className="text-xs text-amber-400">
              You have unsaved changes
            </span>
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
