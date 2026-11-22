import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { useAgentSkills } from '../../../hooks/useAgentSkills'
import type { ScannedSkill, SkillInstallScope } from '../../../lib/types'
import { InstallSkillModal } from '../InstallSkillModal'

type Props = {
  agentId: string
  workspacePath?: string
  isOpen: boolean
  onClose: () => void
}

export function AgentSkillInstallDialog({ agentId, workspacePath, isOpen, onClose }: Props) {
  const { api } = useSettings()
  const { t } = useTranslation()
  const { isInstalling, install, installBatch, error } = useAgentSkills(api, { agentId })

  const [scope, setScope] = useState<SkillInstallScope>('workspace')
  const [scanResults, setScanResults] = useState<ScannedSkill[] | null>(null)
  const [isScanning, setIsScanning] = useState(false)

  const available = useQuery({
    queryKey: ['skills', 'available'],
    queryFn: () => api.availableSkills(),
    enabled: isOpen,
    staleTime: 60_000,
    retry: 1,
  })

  useEffect(() => {
    if (!isOpen) {
      setScanResults(null)
      setIsScanning(false)
    }
  }, [isOpen])

  const handleInstall = async (url: string) => {
    await install(url, scope)
  }

  const handleScan = async (repo: string): Promise<ScannedSkill[] | null> => {
    setIsScanning(true)
    setScanResults(null)
    try {
      const response = await api.scanSkills(repo)
      setScanResults(response.skills)
      return response.skills
    } catch {
      setScanResults(null)
      return null
    } finally {
      setIsScanning(false)
    }
  }

  const handleInstallBatch = async (repo: string, skills: string[]) => {
    const ok = await installBatch(repo, skills, scope)
    if (ok) {
      setScanResults(null)
      onClose()
    }
  }

  return (
    <>
      <InstallSkillModal
        isOpen={isOpen}
        onClose={onClose}
        availableSkills={available.data?.skills ?? []}
        isAvailableLoading={available.isLoading}
        isInstalling={isInstalling}
        isScanning={isScanning}
        scanResults={scanResults}
        scope={scope}
        onScopeChange={setScope}
        workspacePath={workspacePath}
        onInstall={handleInstall}
        onScan={handleScan}
        onInstallBatch={handleInstallBatch}
        onClearScan={() => setScanResults(null)}
      />
      {error && (
        <p
          data-testid="agent-skill-install-error"
          role="alert"
          className="fixed bottom-[max(1.5rem,env(safe-area-inset-bottom))] left-1/2 z-50 -translate-x-1/2 rounded-lg border border-state-error/40 bg-state-error/10 px-4 py-2 text-xs text-state-error"
        >
          {t('settings.agentPage.skillsInstallFailed', {
            defaultValue: 'Could not install the skill: {{message}}',
            message: error,
          })}
        </p>
      )}
    </>
  )
}
