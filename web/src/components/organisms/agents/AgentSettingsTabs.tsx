import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useIsMobile } from '../../../hooks/useIsMobile'
import {
  type AgentTab,
  isSectionDirty,
  pathInPrefix,
  sectionPrefixes,
} from '../../../lib/agentDirty'
import type { ConfigError } from '../../../lib/types'
import {
  CodeIcon,
  FolderIcon,
  McpIcon,
  ServerIcon,
  SettingsIcon,
  SkillsIcon,
  SubagentsIcon,
  TerminalIcon,
} from '../../atoms/Icons'

const TAB_META: Record<AgentTab, { labelKey: string; Icon: typeof SettingsIcon }> = {
  general: { labelKey: 'settings.agentPage.tab.general', Icon: SettingsIcon },
  model: { labelKey: 'settings.agentPage.tab.model', Icon: ServerIcon },
  skills: { labelKey: 'settings.agentPage.tab.skills', Icon: SkillsIcon },
  mcp: { labelKey: 'settings.agentPage.tab.mcp', Icon: McpIcon },
  commands: { labelKey: 'settings.agentPage.tab.commands', Icon: TerminalIcon },
  tools: { labelKey: 'settings.agentPage.tab.tools', Icon: CodeIcon },
  subagents: { labelKey: 'settings.agentPage.tab.subagents', Icon: SubagentsIcon },
  files: { labelKey: 'settings.agentPage.tab.files', Icon: FolderIcon },
}

export const AGENT_TAB_ORDER: AgentTab[] = [
  'general',
  'model',
  'skills',
  'mcp',
  'commands',
  'tools',
  'subagents',
  'files',
]

const RAIL_MIN_WIDTH = 1024

export const AGENT_TAB_PANEL_ID = 'agent-tab-panel'

export function agentTabId(tab: AgentTab): string {
  return `agent-tab-${tab}`
}

export function sectionHasError(errors: ConfigError[], index: number, tab: AgentTab): boolean {
  if (!errors || errors.length === 0) return false
  const prefixes = sectionPrefixes(index, tab)
  return errors.some((error) => prefixes.some((prefix) => pathInPrefix(error.path, prefix)))
}

type DotProps = { hasError: boolean; isDirty: boolean }

function StatusDot({ hasError, isDirty }: DotProps) {
  const { t } = useTranslation()
  if (!hasError && !isDirty) return null
  return (
    <>
      <span
        aria-hidden="true"
        className={`ml-auto h-1.5 w-1.5 flex-shrink-0 rounded-full ${
          hasError ? 'bg-state-error' : 'bg-state-info'
        }`}
        data-testid={hasError ? 'tab-dot-error' : 'tab-dot-dirty'}
      />
      <span className="sr-only">{t('settings.agentPage.sectionHasChanges')}</span>
    </>
  )
}

type Props = {
  agentIndex: number
  activeTab: AgentTab
  dirtyPaths: Set<string>
  validationErrors: ConfigError[]
  onTabChange: (tab: AgentTab) => void
}

export function AgentSettingsTabs({
  agentIndex,
  activeTab,
  dirtyPaths,
  validationErrors,
  onTabChange,
}: Props) {
  const { t } = useTranslation()
  const isHorizontalBar = useIsMobile(RAIL_MIN_WIDTH)
  const tabRefs = useRef<Partial<Record<AgentTab, HTMLButtonElement | null>>>({})

  const activate = (tab: AgentTab) => {
    onTabChange(tab)
    tabRefs.current[tab]?.focus()
  }

  const handleKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>, current: AgentTab) => {
    const position = AGENT_TAB_ORDER.indexOf(current)
    let next: number | null = null
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowRight':
        next = (position + 1) % AGENT_TAB_ORDER.length
        break
      case 'ArrowUp':
      case 'ArrowLeft':
        next = (position - 1 + AGENT_TAB_ORDER.length) % AGENT_TAB_ORDER.length
        break
      case 'Home':
        next = 0
        break
      case 'End':
        next = AGENT_TAB_ORDER.length - 1
        break
      default:
        return
    }
    event.preventDefault()
    activate(AGENT_TAB_ORDER[next])
  }

  return (
    <nav
      role="tablist"
      aria-orientation={isHorizontalBar ? 'horizontal' : 'vertical'}
      aria-label={t('settings.agentPage.tablistLabel')}
      className="w-full flex-shrink-0 overflow-x-auto border-b border-border bg-background-secondary p-2 lg:min-w-0 lg:w-[200px] lg:overflow-x-visible lg:border-b-0 lg:border-r lg:p-4"
    >
      <div className="flex min-w-max gap-1 lg:min-w-0 lg:flex-col lg:space-y-1">
        {AGENT_TAB_ORDER.map((tab) => {
          const selected = tab === activeTab
          const { labelKey, Icon } = TAB_META[tab]
          return (
            <button
              key={tab}
              ref={(node) => {
                tabRefs.current[tab] = node
              }}
              type="button"
              role="tab"
              id={agentTabId(tab)}
              aria-selected={selected}
              aria-controls={AGENT_TAB_PANEL_ID}
              tabIndex={selected ? 0 : -1}
              onClick={() => onTabChange(tab)}
              onKeyDown={(event) => handleKeyDown(event, tab)}
              className={`flex flex-shrink-0 items-center whitespace-nowrap rounded-md border px-3 py-1.5 text-xs font-medium transition-colors lg:py-2.5 lg:text-left lg:text-sm ${
                selected
                  ? 'border-accent-primary/30 bg-surface-selected text-accent-text'
                  : 'border-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary'
              }`}
            >
              <Icon size={14} className="mr-2 opacity-80" />
              {t(labelKey)}
              <StatusDot
                hasError={sectionHasError(validationErrors, agentIndex, tab)}
                isDirty={isSectionDirty(dirtyPaths, agentIndex, tab)}
              />
            </button>
          )
        })}
      </div>
    </nav>
  )
}
