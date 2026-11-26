import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import type { SettingsTab } from '../../lib/settingsTabs'
import {
  ActivityIcon,
  AgentsIcon,
  ClockIcon,
  CodeIcon,
  DownloadIcon,
  FileTextIcon,
  GroupsIcon,
  McpIcon,
  ProvidersIcon,
  SendIcon,
  ServerIcon,
  SettingsIcon,
  SubagentsIcon,
  TerminalIcon,
  ToolsIcon,
} from '../atoms/Icons'

const TAB_ICONS: Record<SettingsTab, typeof SettingsIcon> = {
  general: SettingsIcon,
  agents: AgentsIcon,
  subagents: SubagentsIcon,
  providers: ProvidersIcon,
  session: ClockIcon,
  channels: SendIcon,
  native: TerminalIcon,
  groups: GroupsIcon,
  tools: ToolsIcon,
  mcp: McpIcon,
  system: ServerIcon,
  updates: DownloadIcon,
  advanced: CodeIcon,
  diagnostics: ActivityIcon,
  logs: FileTextIcon,
}

const inset = (value: string): number => Number.parseFloat(value) || 0

type Props = {
  activeTab: SettingsTab
  onTabChange: (tab: SettingsTab) => void
}

export function SettingsTabs({ activeTab, onTabChange }: Props) {
  const { t } = useTranslation()
  const { groupsEnabled } = useAppLogicContext()
  const navRef = useRef<HTMLElement | null>(null)
  const tabRefs = useRef<Partial<Record<SettingsTab, HTMLButtonElement | null>>>({})

  const revealActiveTab = useCallback(() => {
    const scroller = navRef.current
    const button = tabRefs.current[activeTab]
    if (!scroller || !button) return
    const style = window.getComputedStyle(scroller)
    const viewRect = scroller.getBoundingClientRect()
    const buttonRect = button.getBoundingClientRect()
    const viewTop = viewRect.top + inset(style.paddingTop)
    const viewBottom = viewRect.bottom - inset(style.paddingBottom)
    const viewLeft = viewRect.left + inset(style.paddingLeft)
    const viewRight = viewRect.right - inset(style.paddingRight)
    const overflowTop = buttonRect.top - viewTop
    const overflowBottom = buttonRect.bottom - viewBottom
    const overflowLeft = buttonRect.left - viewLeft
    const overflowRight = buttonRect.right - viewRight
    if (overflowTop < 0) scroller.scrollTop += overflowTop
    else if (overflowBottom > 0) scroller.scrollTop += overflowBottom
    if (overflowLeft < 0) scroller.scrollLeft += overflowLeft
    else if (overflowRight > 0) scroller.scrollLeft += overflowRight
  }, [activeTab])

  // biome-ignore lint/correctness/useExhaustiveDependencies: groupsEnabled is the TRIGGER, not a read — the groups tab arrives after mount and moves every later tab, so the reveal must re-measure against the new rail height
  useEffect(() => {
    revealActiveTab()
    window.addEventListener('resize', revealActiveTab)
    return () => window.removeEventListener('resize', revealActiveTab)
  }, [revealActiveTab, groupsEnabled])

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'general', label: t('settings.tabs.general') },
    { id: 'agents', label: t('settings.tabs.agents') },
    { id: 'subagents', label: t('settings.tabs.subagents') },
    { id: 'providers', label: t('settings.tabs.providers') },
    { id: 'session', label: t('settings.tabs.session') },
    { id: 'channels', label: t('settings.tabs.channels') },
    { id: 'native', label: t('settings.tabs.native') },
    ...(groupsEnabled ? [{ id: 'groups' as const, label: t('settings.tabs.groups') }] : []),
    { id: 'tools', label: t('settings.tabs.tools') },
    { id: 'mcp', label: t('settings.tabs.mcp') },
    { id: 'system', label: t('settings.tabs.system') },
    { id: 'updates', label: t('settings.tabs.updates') },
    { id: 'advanced', label: t('settings.tabs.advanced') },
    { id: 'diagnostics', label: t('settings.tabs.diagnostics') },
    { id: 'logs', label: t('settings.tabs.logs') },
  ]

  return (
    <nav
      ref={navRef}
      data-testid="settings-tabs"
      className="w-full md:w-[200px] flex-shrink-0 border-b md:border-b-0 md:border-r border-border bg-background-secondary p-2 md:p-4 overflow-x-auto md:overflow-x-visible md:overflow-y-auto"
    >
      <div className="flex md:flex-col gap-1 md:space-y-1 min-w-max md:min-w-0">
        {tabs.map((tab) => {
          const Icon = TAB_ICONS[tab.id]
          return (
            <button
              key={tab.id}
              ref={(element) => {
                tabRefs.current[tab.id] = element
              }}
              onClick={() => onTabChange(tab.id)}
              type="button"
              data-testid={`settings-tab-${tab.id}`}
              aria-current={activeTab === tab.id ? 'page' : undefined}
              className={`flex items-center rounded-md border px-3 py-1.5 md:py-2.5 text-center md:text-left text-xs md:text-sm font-medium transition-colors whitespace-nowrap flex-shrink-0 ${
                activeTab === tab.id
                  ? 'border-accent-primary/30 bg-surface-selected text-accent-text'
                  : 'border-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary'
              }`}
            >
              <Icon size={14} className="mr-2 flex-shrink-0 opacity-80" />
              {tab.label}
            </button>
          )
        })}
      </div>
    </nav>
  )
}
