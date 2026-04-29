import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { AvailableSkill } from '../../lib/types'
import { Modal, Spinner } from '../atoms'

type Props = {
  isOpen: boolean
  onClose: () => void
  availableSkills: AvailableSkill[]
  isAvailableLoading: boolean
  isInstalling: boolean
  onInstall: (url: string) => void
}

export function InstallSkillModal({
  isOpen,
  onClose,
  availableSkills,
  isAvailableLoading,
  isInstalling,
  onInstall,
}: Props) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState<'browse' | 'url'>('browse')
  const [repoUrl, setRepoUrl] = useState('')
  const [searchQuery, setSearchQuery] = useState('')

  const filteredSkills = availableSkills.filter((skill) => {
    if (!searchQuery.trim()) return true
    const q = searchQuery.toLowerCase()
    return (
      skill.name.toLowerCase().includes(q) ||
      skill.description.toLowerCase().includes(q) ||
      skill.author.toLowerCase().includes(q) ||
      skill.tags.some((tag) => tag.toLowerCase().includes(q))
    )
  })

  const handleInstall = (url: string) => {
    onInstall(url)
  }

  const handleUrlInstall = () => {
    if (repoUrl.trim()) {
      onInstall(repoUrl.trim())
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={t('skills.installSkill', 'Install Skill')}
      size="lg"
    >
      <div className="flex flex-col">
        {/* Tab switcher */}
        <div className="flex border-b border-border px-6 pt-2">
          <button
            type="button"
            onClick={() => setActiveTab('browse')}
            className={`px-4 py-2 text-sm border-b-2 transition-colors ${
              activeTab === 'browse'
                ? 'border-brand-rosa text-brand-rosa'
                : 'border-transparent text-text-secondary hover:text-text-primary'
            }`}
          >
            {t('skills.browse', 'Browse')}
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('url')}
            className={`px-4 py-2 text-sm border-b-2 transition-colors ${
              activeTab === 'url'
                ? 'border-brand-rosa text-brand-rosa'
                : 'border-transparent text-text-secondary hover:text-text-primary'
            }`}
          >
            {t('skills.installFromUrl', 'Install from URL')}
          </button>
        </div>

        {/* Browse tab */}
        {activeTab === 'browse' && (
          <div className="flex flex-col">
            <div className="px-6 pt-4 pb-2">
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder={t('skills.searchSkills', 'Search skills...')}
                className="w-full rounded-lg border border-border bg-background-secondary px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-2 focus:ring-brand-rosa/30 focus:border-brand-rosa/50"
              />
            </div>

            <div className="max-h-80 overflow-y-auto px-6 pb-4">
              {isAvailableLoading ? (
                <div className="flex items-center justify-center py-12">
                  <Spinner />
                  <span className="ml-3 text-sm text-text-secondary">{t('common.loading')}</span>
                </div>
              ) : filteredSkills.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-12 text-center">
                  <p className="text-sm text-text-secondary">
                    {searchQuery.trim()
                      ? t('skills.noSearchResults', 'No skills match your search')
                      : t('skills.noAvailableSkills', 'No available skills found')}
                  </p>
                </div>
              ) : (
                <div className="space-y-3 pt-2">
                  {filteredSkills.map((skill) => (
                    <div
                      key={skill.name}
                      className="flex items-start justify-between rounded-lg border border-border bg-background-secondary/50 p-3 hover:border-brand-rosa/30 transition-colors"
                    >
                      <div className="flex-1 min-w-0 mr-3">
                        <div className="flex items-center gap-2">
                          <h4 className="text-sm font-medium text-text-primary">{skill.name}</h4>
                          {skill.author && (
                            <span className="text-xs text-text-tertiary">by {skill.author}</span>
                          )}
                        </div>
                        <p className="mt-1 text-xs text-text-secondary line-clamp-2">
                          {skill.description}
                        </p>
                        {skill.tags.length > 0 && (
                          <div className="mt-2 flex flex-wrap gap-1">
                            {skill.tags.map((tag) => (
                              <span
                                key={tag}
                                className="inline-block rounded-full bg-background-tertiary px-2 py-0.5 text-[10px] text-text-tertiary"
                              >
                                {tag}
                              </span>
                            ))}
                          </div>
                        )}
                      </div>
                      <button
                        type="button"
                        onClick={() => handleInstall(skill.repository)}
                        disabled={isInstalling}
                        className="flex-shrink-0 rounded-lg bg-brand-rosa/10 px-3 py-1.5 text-xs font-medium text-brand-rosa hover:bg-brand-rosa/20 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                      >
                        {isInstalling
                          ? t('skills.installing', 'Installing...')
                          : t('skills.install', 'Install')}
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}

        {/* URL tab */}
        {activeTab === 'url' && (
          <div className="flex flex-col gap-4 p-6">
            <div>
              <label
                htmlFor="github-repo-input"
                className="block text-sm font-medium text-text-primary mb-1.5"
              >
                {t('skills.githubRepo', 'GitHub repository')}
              </label>
              <input
                id="github-repo-input"
                type="text"
                value={repoUrl}
                onChange={(e) => setRepoUrl(e.target.value)}
                placeholder="sipeed/lele-skills/weather"
                className="w-full rounded-lg border border-border bg-background-secondary px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-2 focus:ring-brand-rosa/30 focus:border-brand-rosa/50"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleUrlInstall()
                }}
              />
              <p className="mt-1.5 text-xs text-text-tertiary">
                {t('skills.githubRepoHint', 'e.g. sipeed/lele-skills/weather')}
              </p>
            </div>

            <div className="flex justify-end">
              <button
                type="button"
                onClick={handleUrlInstall}
                disabled={isInstalling || !repoUrl.trim()}
                className="rounded-lg bg-brand-rosa px-4 py-2 text-sm font-medium text-white hover:bg-brand-rosa/90 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {isInstalling
                  ? t('skills.installing', 'Installing...')
                  : t('skills.install', 'Install')}
              </button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
