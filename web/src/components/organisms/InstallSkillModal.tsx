import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { AvailableSkill, ScannedSkill } from '../../lib/types'
import { Button, Modal, Spinner } from '../atoms'

type Props = {
  isOpen: boolean
  onClose: () => void
  availableSkills: AvailableSkill[]
  isAvailableLoading: boolean
  isInstalling: boolean
  isScanning: boolean
  scanResults: ScannedSkill[] | null
  onInstall: (url: string) => void
  onScan: (repo: string) => Promise<ScannedSkill[] | null>
  onInstallBatch: (repo: string, skills: string[]) => void
  onClearScan: () => void
}

export function InstallSkillModal({
  isOpen,
  onClose,
  availableSkills,
  isAvailableLoading,
  isInstalling,
  isScanning,
  scanResults,
  onInstall,
  onScan,
  onInstallBatch,
  onClearScan,
}: Props) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState<'browse' | 'url'>('browse')
  const [repoUrl, setRepoUrl] = useState('')
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedSkills, setSelectedSkills] = useState<Set<string>>(new Set())
  const [scannedRepo, setScannedRepo] = useState<string | null>(null)

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

  const handleScan = async () => {
    if (!repoUrl.trim()) return
    setSelectedSkills(new Set())
    setScannedRepo(repoUrl.trim())
    const results = await onScan(repoUrl.trim())
    if (results) {
      // Pre-select all skills
      setSelectedSkills(new Set(results.map((s) => s.path)))
    }
  }

  const handleToggleSkill = (path: string) => {
    setSelectedSkills((prev) => {
      const next = new Set(prev)
      if (next.has(path)) {
        next.delete(path)
      } else {
        next.add(path)
      }
      return next
    })
  }

  const handleInstallSelected = () => {
    if (scannedRepo && selectedSkills.size > 0) {
      onInstallBatch(scannedRepo, Array.from(selectedSkills))
    }
  }

  const handleClose = () => {
    onClearScan()
    setRepoUrl('')
    setSearchQuery('')
    setSelectedSkills(new Set())
    setScannedRepo(null)
    onClose()
  }

  // If scan results are available, show the picker
  if (scanResults && scanResults.length > 0) {
    return (
      <Modal
        isOpen={isOpen}
        onClose={handleClose}
        title={t('skills.selectSkills', 'Select Skills to Install')}
        size="lg"
      >
        <div className="flex flex-col p-6">
          <p className="text-sm text-text-secondary mb-4">
            {t('skills.foundSkills', 'Found {{count}} skills in', { count: scanResults.length })}{' '}
            <span className="font-medium text-text-primary">{scannedRepo}</span>
          </p>

          <div className="space-y-2 max-h-60 overflow-y-auto">
            {scanResults.map((skill) => (
              <label
                key={skill.path}
                className="flex items-start gap-3 rounded-xl border border-border bg-background-secondary p-3 hover:border-brand-rosa/30 transition-colors cursor-pointer"
              >
                <input
                  type="checkbox"
                  checked={selectedSkills.has(skill.path)}
                  onChange={() => handleToggleSkill(skill.path)}
                  className="mt-0.5 h-4 w-4 rounded border-border text-brand-rosa focus:ring-brand-rosa/30"
                />
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium text-text-primary">{skill.name}</div>
                  {skill.description && (
                    <p className="mt-0.5 text-xs text-text-secondary line-clamp-2">
                      {skill.description}
                    </p>
                  )}
                  <p className="mt-1 text-[10px] text-text-tertiary">{skill.path}</p>
                </div>
              </label>
            ))}
          </div>

          <div className="flex justify-between items-center mt-4">
            <button
              type="button"
              onClick={() => {
                onClearScan()
                setScannedRepo(null)
                setSelectedSkills(new Set())
              }}
              className="text-sm text-text-secondary hover:text-text-primary transition-colors"
            >
              ← {t('common.back', 'Back')}
            </button>
            <Button
              variant="primary"
              size="md"
              onClick={handleInstallSelected}
              loading={isInstalling}
              disabled={selectedSkills.size === 0}
              type="button"
            >
              {isInstalling
                ? t('skills.installing', 'Installing...')
                : t('skills.installSelected', 'Install {{count}} skills', {
                    count: selectedSkills.size,
                  })}
            </Button>
          </div>
        </div>
      </Modal>
    )
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClose}
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
                      className="flex items-start justify-between rounded-xl border border-border bg-background-secondary p-3 hover:border-brand-rosa/30 transition-colors"
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
                placeholder="sipeed/lele-skills"
                className="w-full rounded-lg border border-border bg-background-secondary px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-2 focus:ring-brand-rosa/30 focus:border-brand-rosa/50"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleScan()
                }}
              />
              <p className="mt-1.5 text-xs text-text-tertiary">
                {t(
                  'skills.githubRepoHint',
                  'e.g. sipeed/lele-skills — scan for multiple skills or install a single skill',
                )}
              </p>
            </div>

            {/* Scanning indicator */}
            {isScanning && (
              <div className="flex items-center justify-center py-4">
                <Spinner />
                <span className="ml-3 text-sm text-text-secondary">
                  {t('skills.scanning', 'Scanning repository...')}
                </span>
              </div>
            )}

            <div className="flex justify-end gap-2">
              <Button
                variant="secondary"
                size="md"
                onClick={handleScan}
                loading={isScanning}
                disabled={isInstalling || !repoUrl.trim()}
                type="button"
              >
                {isScanning ? t('skills.scanning', 'Scanning...') : t('skills.scan', 'Scan')}
              </Button>
              <Button
                variant="primary"
                size="md"
                onClick={handleUrlInstall}
                loading={isInstalling}
                disabled={!repoUrl.trim()}
                type="button"
              >
                {isInstalling
                  ? t('skills.installing', 'Installing...')
                  : t('skills.install', 'Install')}
              </Button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
