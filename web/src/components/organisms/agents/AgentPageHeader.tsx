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

/**
 * Header of the per-agent config page (spec §4.2).
 *
 * Breadcrumb (back + "Agents" / id) above an identity row: 48px avatar, name
 * with the same badges the list card uses, a one-line meta summary
 * (`model · temp · thinking · workspace`) and the description.
 *
 * Deliberate omissions:
 * - **No local Save.** The global `SettingsFooter` in `AgentEntityLayout` is
 *   the only place that persists (§4.2.3 — brief requirement).
 * - **"Open chat" hidden, not disabled.** §4.2.3 allows that button only when
 *   a real per-agent chat entry point exists; `AppLogicContext.onCreateSession`
 *   takes no agentId today, so `CAN_OPEN_CHAT_WITH_AGENT` is false and the
 *   action is not rendered (a button that does nothing is noise). Flipping the
 *   constant is all the backend will need — no layout change.
 */

/** Gate for the "Open chat with this agent" action (§4.2.3). */
export const CAN_OPEN_CHAT_WITH_AGENT = false

/** One piece of the meta line: shortened text + full value as tooltip. */
type Segment = { key: string; text: string; title: string }

type Props = {
  agent: EditableAgentConfig
  /** Any dirty path starts with `agents.list.{index}.` (§5.3). */
  isModified: boolean
  /** `agents.defaults.model` — the "inherits" badge names it. */
  defaultsModel: string
}

export function AgentPageHeader({ agent, isModified, defaultsModel }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const displayName = agent.name?.trim() || agent.id
  const primary = getAgentModelPrimary(agent.model)
  const inherited = !primary && !!defaultsModel

  const segments: Segment[] = []

  // The model is always informative: an explicit one, or the inherited one
  // prefixed with `≈` so the line never reads as a choice (§3.4.4).
  if (primary) {
    segments.push({ key: 'model', text: shortModelName(primary), title: primary })
  } else if (inherited) {
    segments.push({
      key: 'model',
      text: `≈ ${shortModelName(defaultsModel)}`,
      title: t('settings.agentPage.inheritsFromDefaults', { model: defaultsModel }),
    })
  }

  // Unset fields are left out instead of spelled as "inherits": the line is a
  // single truncated row, and `≈` already says it for the model.
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
      {/* 1 — breadcrumb. Going back keeps the draft: it lives in the parent. */}
      <div className="mb-2 flex items-center gap-1.5 text-xs">
        <IconButton
          variant="ghost"
          className="h-6 w-6 flex-none p-0"
          ariaLabel={t('settings.agentPage.breadcrumb')}
          onClick={() => navigate('/agents')}
        >
          <ChevronLeftIcon size={14} />
        </IconButton>
        <Link to="/agents" className="text-text-tertiary transition-colors hover:text-text-primary">
          {t('settings.agentPage.breadcrumb')}
        </Link>
        <span aria-hidden="true" className="text-text-muted">
          /
        </span>
        <span aria-current="page" className="truncate font-mono text-text-secondary">
          {agent.id}
        </span>
      </div>

      {/* 2 — identity row */}
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

        {/* 3 — actions (hidden until a per-agent chat entry point exists) */}
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
