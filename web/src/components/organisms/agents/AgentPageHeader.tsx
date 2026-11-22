import { useTranslation } from 'react-i18next'
import { Link, useNavigate } from 'react-router-dom'
import { getAgentModelPrimary } from '../../../hooks/useSettingsHelpers'
import { shortModelName } from '../../../lib/modelName'
import { expandHomeDisplay } from '../../../lib/paths'
import { thinkingLevelLabel } from '../../../lib/thinkingLevel'
import type { EditableAgentConfig } from '../../../lib/types'
import { AgentAvatar } from '../../atoms/AgentAvatar'
import { Button } from '../../atoms/Button'
import { IconButton } from '../../atoms/IconButton'
import { ChatBubbleIcon, ChevronLeftIcon } from '../../atoms/Icons'
import { AgentBadges, MetaDot } from './AgentBadges'

export const CAN_OPEN_CHAT_WITH_AGENT = false

type Segment = { key: string; text: string; title: string }

type Props = {
  agent: EditableAgentConfig
  isModified: boolean
  defaultsModel: string
}

export function AgentPageHeader({ agent, isModified, defaultsModel }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const displayName = agent.name?.trim() || agent.id
  const primary = getAgentModelPrimary(agent.model)
  const inherited = !primary && !!defaultsModel

  const segments: Segment[] = []

  if (primary) {
    segments.push({ key: 'model', text: shortModelName(primary), title: primary })
  } else if (inherited) {
    segments.push({
      key: 'model',
      text: `≈ ${shortModelName(defaultsModel)}`,
      title: t('settings.agentPage.inheritsFromDefaults', { model: defaultsModel }),
    })
  }

  if (typeof agent.temperature === 'number') {
    segments.push({
      key: 'temperature',
      text: `temp ${agent.temperature}`,
      title: `temperature = ${agent.temperature}`,
    })
  }
  if (agent.thinking_level) {
    segments.push({
      key: 'thinking',
      text: `thinking ${agent.thinking_level}`,
      title: thinkingLevelLabel(t, agent.thinking_level),
    })
  }
  if (agent.workspace) {
    const expanded = expandHomeDisplay(agent.workspace)
    segments.push({ key: 'workspace', text: expanded, title: expanded })
  }

  return (
    <header className="mb-5 border-b border-border pb-4">
      <div className="mb-2 flex items-center gap-1.5 text-xs">
        <IconButton
          variant="ghost"
          className="h-6 w-6 flex-none p-0"
          ariaLabel={t('settings.agentPage.breadcrumb')}
          onClick={() => navigate('/settings/agents')}
        >
          <ChevronLeftIcon size={14} />
        </IconButton>
        <Link
          to="/settings/agents"
          className="text-text-tertiary transition-colors hover:text-text-primary"
        >
          {t('settings.agentPage.breadcrumb')}
        </Link>
        <span aria-hidden="true" className="text-text-muted">
          /
        </span>
        <span aria-current="page" className="truncate font-mono text-text-secondary">
          {agent.id}
        </span>
      </div>

      <div className="flex items-start gap-3">
        <AgentAvatar id={agent.id} name={agent.name} size="xl" className="flex-none" />

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <h1 className="truncate text-xl font-semibold text-text-primary">{displayName}</h1>
            <AgentBadges agent={agent} isModified={isModified} defaultsModel={defaultsModel} />
          </div>

          {segments.length > 0 && (
            <div
              data-testid="agent-header-meta"
              className="mt-1 truncate text-xs text-text-tertiary"
            >
              {segments.map((segment, position) => (
                <span key={segment.key} className="contents">
                  {position > 0 && <MetaDot />}
                  <span title={segment.title}>{segment.text}</span>
                </span>
              ))}
            </div>
          )}

          {agent.description && (
            <p className="mt-0.5 truncate text-xs text-text-secondary" title={agent.description}>
              {agent.description}
            </p>
          )}
        </div>

        {CAN_OPEN_CHAT_WITH_AGENT && (
          <div className="ml-auto flex flex-none items-center gap-2">
            <Button
              variant="secondary"
              size="md"
              onClick={() => navigate(`/chat?agent=${encodeURIComponent(agent.id)}`)}
            >
              <ChatBubbleIcon size={14} />
              {t('settings.agentPage.openChat', { defaultValue: 'Open chat' })}
            </Button>
          </div>
        )}
      </div>
    </header>
  )
}
