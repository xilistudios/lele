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
  ServerIcon,
  SettingsIcon,
  SkillsIcon,
  SubagentsIcon,
} from '../../atoms/Icons'

/**
 * Tab strip of `/agents/:agentId/:tab` (spec §1.3 / §4.1 / §5.3 / §5.4 / §6 / §8).
 *
 * A faithful copy of `SettingsTabs` — same nav/button classes, same collapse to
 * a horizontally scrollable bar on narrow screens — plus the two differences
 * the spec asks for:
 *
 * 1. a 14px icon per tab (§4.1 "Diferencia 1"): six sections of very different
 *    nature are recognised faster by icon than by text alone;
 * 2. a status dot per tab (§4.1 "Diferencia 2", §5.3/§5.4): blue when the
 *    section has unsaved changes, red when it holds a validation error.
 *
 * Breakpoint deviation, documented per §8: `SettingsTabs` switches to the
 * lateral rail at `md` (768px); here the rail appears at `lg` (1024px), because
 * this page shares the viewport with the app Sidebar (280px expanded) and at
 * 768px the panel would be left with ~288px of content.
 *
 * Keyboard follows the WAI-ARIA "tabs with automatic activation" pattern (§6):
 * arrows move focus AND activate (each tab is a route, there are no hidden
 * panels), Home/End jump to the ends, `tabIndex` roves. Orientation follows the
 * layout: `vertical` on the rail, `horizontal` in the bar.
 */

/** Tab labels are `settings.agentPage.tab.<id>` — already present in all locales. */
const TAB_META: Record<AgentTab, { labelKey: string; Icon: typeof SettingsIcon }> = {
  general: { labelKey: 'settings.agentPage.tab.general', Icon: SettingsIcon },
  model: { labelKey: 'settings.agentPage.tab.model', Icon: ServerIcon },
  skills: { labelKey: 'settings.agentPage.tab.skills', Icon: SkillsIcon },
  tools: { labelKey: 'settings.agentPage.tab.tools', Icon: CodeIcon },
  subagents: { labelKey: 'settings.agentPage.tab.subagents', Icon: SubagentsIcon },
  files: { labelKey: 'settings.agentPage.tab.files', Icon: FolderIcon },
}

/** Stable order for arrows / Home / End — identical to `AGENT_TABS`. */
export const AGENT_TAB_ORDER: AgentTab[] = [
  'general',
  'model',
  'skills',
  'tools',
  'subagents',
  'files',
]

/** Below this viewport width the rail becomes a horizontal scrollable bar (§8). */
const RAIL_MIN_WIDTH = 1024

/** `id` of the panel this strip controls; the panel back-references each tab. */
export const AGENT_TAB_PANEL_ID = 'agent-tab-panel'

/** `id` of a tab button, used by the panel's `aria-labelledby`. */
export function agentTabId(tab: AgentTab): string {
  return `agent-tab-${tab}`
}

/**
 * Does any validation error fall inside this agent+section? (§5.4)
 *
 * Matching is by prefix, so an error on `…model.primary` lights the `model`
 * tab. Errors above the section (e.g. on the whole `agents.list`, such as a
 * duplicate id) intentionally light nothing: the footer already reports them
 * globally, and painting six red dots would be noise.
 */
export function sectionHasError(errors: ConfigError[], index: number, tab: AgentTab): boolean {
  if (!errors || errors.length === 0) return false
  const prefixes = sectionPrefixes(index, tab)
  return errors.some((error) => prefixes.some((prefix) => pathInPrefix(error.path, prefix)))
}

type DotProps = { hasError: boolean; isDirty: boolean }

/** Status indicator: priority `error > dirty > nothing` (§5.4). */
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
      {/* Never colour-only (§10.5). */}
      <span className="sr-only">{t('settings.agentPage.sectionHasChanges')}</span>
    </>
  )
}

type Props = {
  /** Position of the agent in `agents.list`; dirty prefixes are positional. */
  agentIndex: number
  activeTab: AgentTab
  dirtyPaths: Set<string>
  /** Server/validate errors — a section holding one paints its dot red (§5.4). */
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
  // `useIsMobile` answers "narrower than the breakpoint?", which here means
  // "show the horizontal bar, not the rail".
  const isHorizontalBar = useIsMobile(RAIL_MIN_WIDTH)
  const tabRefs = useRef<Partial<Record<AgentTab, HTMLButtonElement | null>>>({})

  const activate = (tab: AgentTab) => {
    onTabChange(tab)
    tabRefs.current[tab]?.focus()
  }

  const handleKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>, current: AgentTab) => {
    const position = AGENT_TAB_ORDER.indexOf(current)
    // Down/Right advance, Up/Left go back. Both pairs work in both layouts: the
    // physical direction changes with the layout and a single mapping is easier
    // to remember than one that swaps on resize (§6).
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
      className="w-full flex-shrink-0 overflow-x-auto border-b border-border bg-background-secondary p-2 no-scrollbar lg:min-w-0 lg:w-[200px] lg:overflow-x-visible lg:border-b-0 lg:border-r lg:p-4"
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
                  ? 'border-[color-mix(in_srgb,var(--color-accent-primary)_30%,transparent)] bg-surface-selected text-accent-primary'
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
