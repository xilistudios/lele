import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useSkills } from '../../hooks/useSkills'
import { InstallSkillModal } from '../organisms/InstallSkillModal'
import { Sidebar } from '../organisms/Sidebar'
import { SkillsList } from '../organisms/SkillsList'

export function SkillsPage() {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()
  const {
    skills,
    availableSkills,
    isLoading,
    isAvailableLoading,
    isInstalling,
    isRemoving,
    isScanning,
    scanResults,
    error,
    fetchAvailableSkills,
    installSkill,
    removeSkill,
    scanRepo,
    installBatch,
    toggleSkill,
    clearScanResults,
  } = useSkills(api)

  const [modalOpen, setModalOpen] = useState(false)

  const handleOpenModal = useCallback(() => {
    setModalOpen(true)
    fetchAvailableSkills()
  }, [fetchAvailableSkills])

  const handleCloseModal = useCallback(() => {
    setModalOpen(false)
  }, [])

  const handleInstall = useCallback(
    async (url: string) => {
      await installSkill(url)
    },
    [installSkill],
  )

  const handleScan = useCallback(
    async (repo: string) => {
      return await scanRepo(repo)
    },
    [scanRepo],
  )

  const handleInstallBatch = useCallback(
    async (repo: string, skills: string[]) => {
      await installBatch(repo, skills)
      setModalOpen(false)
    },
    [installBatch],
  )

  const handleToggle = useCallback(
    async (name: string, enabled: boolean) => {
      await toggleSkill(name, enabled)
    },
    [toggleSkill],
  )

  return (
    <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={sidebarOpen}
        onClose={() => onToggleSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        {/* Header */}
        <header className="flex items-center justify-between border-b border-border px-6 py-4 shrink-0 bg-background-secondary/30">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onToggleSidebar()}
              className="flex md:hidden p-1.5 rounded-lg text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-colors"
              title={t('chat.toggleSidebar')}
            >
              {' '}
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>Toggle sidebar</title>
                <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
                <line x1="9" y1="3" x2="9" y2="21" />
              </svg>
            </button>
            <h1 className="text-base font-semibold text-text-primary">
              {t('skills.title', 'Skills')}
            </h1>
          </div>
          <button
            type="button"
            onClick={handleOpenModal}
            className="inline-flex items-center gap-1.5 rounded-lg bg-brand-rosa px-3 py-2 text-sm font-medium text-white hover:bg-brand-rosa/90 transition-colors"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <title>Install</title>
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('skills.installSkill', 'Install Skill')}
          </button>
        </header>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6">
          {error && (
            <div className="mb-4 rounded-lg border border-state-error/30 bg-state-error-light px-4 py-3 text-sm text-state-error">
              {error}
            </div>
          )}

          {!isLoading && skills.length > 0 && (
            <p className="mb-4 text-xs text-text-tertiary">
              {skills.length} {skills.length === 1 ? 'skill' : 'skills'}{' '}
              {t('skills.installed', 'installed')}
            </p>
          )}

          <SkillsList
            skills={skills}
            isLoading={isLoading}
            isRemoving={isRemoving}
            onRemove={removeSkill}
            onToggle={handleToggle}
          />
        </div>
      </main>

      <InstallSkillModal
        isOpen={modalOpen}
        onClose={handleCloseModal}
        availableSkills={availableSkills}
        isAvailableLoading={isAvailableLoading}
        isInstalling={isInstalling}
        isScanning={isScanning}
        scanResults={scanResults}
        onInstall={handleInstall}
        onScan={handleScan}
        onInstallBatch={handleInstallBatch}
        onClearScan={clearScanResults}
      />
    </div>
  )
}
