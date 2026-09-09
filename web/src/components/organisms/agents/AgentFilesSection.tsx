import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useSettings } from '../../../contexts/SettingsContext'
import { formatBytes } from '../../../lib/format'
import type { EditableAgentConfig } from '../../../lib/types'
import { Badge } from '../../atoms/Badge'
import { Button } from '../../atoms/Button'
import { ChevronRightIcon } from '../../atoms/Icons'
import { SettingsSection } from '../../molecules/SettingsSection'

/**
 * `tab=files` — read-only summary of the agent workspace (spec §4.8).
 *
 * The editor itself is `AgentFilesPage` (`/settings/agent/:agentId/:fileName?`),
 * already on main; this section must NOT re-implement it. It answers one
 * question — "what lives in this workspace, and how do I get to it?" — with the
 * workspace path, a CTA to the editor and one chip per file.
 *
 * It never writes config, so it can never be dirty (§5.3: files has no dot).
 */

type Props = {
  agent: EditableAgentConfig
  /** Real id from the draft (not the URL param), so casing cannot drift. */
  agentId: string
}

/** Query key of the file list. Exported for tests and future invalidation. */
export const agentFilesQueryKey = (agentId: string) => ['agentFiles', agentId] as const

/** Route of the editor for one file of this agent. */
const editorPath = (agentId: string, name?: string) =>
  name ? `/settings/agent/${agentId}/${encodeURIComponent(name)}` : `/settings/agent/${agentId}`

const CHIP_CLS =
  'inline-flex items-center gap-2 rounded-md border border-border bg-background-secondary px-2.5 py-1.5 font-mono text-xs text-text-secondary transition-colors duration-fast hover:border-border-strong'

/** §5.1: one card skeleton with the geometry of the real card. */
function FilesSkeleton() {
  const { t } = useTranslation()
  return (
    // biome-ignore lint/a11y/useSemanticElements: spec §5.1 requires a status live region around the skeleton; <output> is form-only and would not announce a load
    <div role="status" aria-busy="true" data-testid="files-loading" className="space-y-4">
      <span className="sr-only">{t('common.loading')}</span>
      <div className="rounded-lg border border-border bg-background-primary p-4 md:p-6">
        <div className="animate-pulse">
          <div className="h-4 w-1/4 rounded-md bg-background-tertiary" />
          <div className="mt-4 h-9 w-full rounded-md bg-background-tertiary" />
          <div className="mt-4 flex gap-2">
            <div className="h-7 w-28 rounded-md bg-background-tertiary" />
            <div className="h-7 w-24 rounded-md bg-background-tertiary" />
            <div className="h-7 w-20 rounded-md bg-background-tertiary" />
          </div>
        </div>
      </div>
    </div>
  )
}

export function AgentFilesSection({ agent, agentId }: Props) {
  const { t, api } = useSettings()

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: agentFilesQueryKey(agentId),
    queryFn: () => api.agentFiles(agentId),
    staleTime: 10_000,
    retry: 1,
  })

  // Error FIRST: a retry refetches, and answering that with the skeleton again
  // would swap a readable banner for a fake card (§5.1: no layout jumps).
  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-background-secondary/50 px-4 py-8 text-center">
        <p className="text-sm text-text-secondary">{t('settings.agentPage.filesLoadError')}</p>
        <Button variant="secondary" size="sm" data-testid="files-retry" onClick={() => refetch()}>
          {t('settings.agentPage.retry')}
        </Button>
      </div>
    )
  }

  if (isLoading) return <FilesSkeleton />

  const files = data?.files ?? []

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.agentPage.filesTitle')}
        description={t('settings.agentPage.filesHint')}
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0 flex-1">
            <p className="text-xs font-medium text-text-secondary">
              {t('settings.agentPage.workspace')}
            </p>
            <p
              className="mt-1 truncate font-mono text-xs text-text-primary"
              title={agent.workspace || undefined}
              data-testid="files-workspace"
            >
              {agent.workspace || t('settings.agentPage.workspaceInherited')}
            </p>
          </div>
          <Link to={editorPath(agentId)}>
            <Button variant="secondary" size="md" data-testid="files-open">
              {t('settings.agentPage.filesOpen')}
              <ChevronRightIcon size={14} />
            </Button>
          </Link>
        </div>

        {files.length === 0 ? (
          <div
            className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center"
            data-testid="files-empty"
          >
            <p className="text-sm text-text-secondary">{t('settings.agentPage.filesEmpty')}</p>
            <p className="max-w-md text-xs text-text-tertiary">
              {t('settings.agentPage.filesEmptyHint')}
            </p>
          </div>
        ) : (
          <div className="flex flex-wrap gap-2" data-testid="files-list">
            {files.map((file) => (
              <Link
                key={file.name}
                to={editorPath(agentId, file.name)}
                data-testid={`files-chip-${file.name}`}
                className={CHIP_CLS}
              >
                <span className="truncate max-w-48">{file.name}</span>
                <Badge variant="default" size="sm">
                  {formatBytes(file.size)}
                </Badge>
              </Link>
            ))}
          </div>
        )}
      </SettingsSection>
    </div>
  )
}
