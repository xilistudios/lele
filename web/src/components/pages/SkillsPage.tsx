import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useSkills } from '../../hooks/useSkills'
import { SkillsList } from '../organisms/SkillsList'
import { InstallSkillModal } from '../organisms/InstallSkillModal'
import { Sidebar } from '../organisms/Sidebar'

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
    error,
    fetchAvailableSkills,
    installSkill,
    removeSkill,
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
              className="p-1.5 rounded-lg text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-colors"
              title={t('chat.toggleSidebar')}
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
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
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('skills.installSkill', 'Install Skill')}
          </button>
        </header>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-6">
          {error && (
            <div className="mb-4 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-400">
              {error}
            </div>
          )}

          {!isLoading && skills.length > 0 && (
            <p className="mb-4 text-xs text-text-tertiary">
              {skills.length} {skills.length === 1 ? 'skill' : 'skills'} {t('skills.installed', 'installed')}
            </p>
          )}

          <SkillsList
            skills={skills}
            isLoading={isLoading}
            isRemoving={isRemoving}
            onRemove={removeSkill}
          />
        </div>
      </main>

      <InstallSkillModal
        isOpen={modalOpen}
        onClose={handleCloseModal}
        availableSkills={availableSkills}
        isAvailableLoading={isAvailableLoading}
        isInstalling={isInstalling}
        onInstall={handleInstall}
      />
    </div>
  )
}
