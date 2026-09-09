import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { useAgentSkills } from '../../../hooks/useAgentSkills'
import type { ScannedSkill, SkillInstallScope } from '../../../lib/types'
import { InstallSkillModal } from '../InstallSkillModal'

/**
 * Install dialog for ONE agent's workspace.
 *
 * It wraps the existing `InstallSkillModal` (browse / URL / scan picker) and
 * supplies the two things that dialog cannot know on its own:
 *
 *  - the SCOPE: install into this agent's own `<workspace>/skills` (default) or
 *    into the shared global directory. The Skills page never passes a scope, so
 *    that page keeps its previous single-destination behaviour untouched.
 *  - the CALLS: per-agent endpoints through `useAgentSkills`, which invalidates
 *    the agent's catalog query so the grid shows the new skill as soon as the
 *    request resolves.
 *
 * The workspace path is printed by the modal's scope picker because "workspace"
 * is meaningless without knowing WHICH workspace — that ambiguity is the bug
 * this feature fixes.
 */

type Props = {
  /** Agent being edited; every install targets this agent. */
  agentId: string
  /** Directory shown next to the workspace option ("" until the catalog loads). */
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

  // The catalogue of published skills is global (it is a registry, not agent
  // state), so the shared endpoint is reused; it is fetched only while the
  // dialog is open.
  const available = useQuery({
    queryKey: ['skills', 'available'],
    queryFn: () => api.availableSkills(),
    enabled: isOpen,
    staleTime: 60_000,
    retry: 1,
  })

  // A failed install must stay visible until the dialog is dismissed, but must
  // never survive it: reopening with a stale error reads as "still broken".
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
      // Scanning reads the remote repository, which does not depend on the
      // agent, so the shared endpoint serves both pages.
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
          className="fixed bottom-6 left-1/2 z-50 -translate-x-1/2 rounded-lg border border-state-error/40 bg-state-error-light px-4 py-2 text-xs text-state-error"
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
