import { type ReactNode, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

/**
 * Collapsible category group (spec §7.4) — header of each tool category in the
 * Tools tab.
 *
 * Uses the same open/close mechanism as `NamedItemCard` (a CSS grid whose row
 * template animates between `0fr` and `1fr`), so collapsing stays consistent
 * with the rest of the settings UI and needs no measured heights.
 */

type Props = {
  title: string
  activeCount: number
  totalCount: number
  /** Default true: users need the whole state at a glance (§4.6.4). */
  defaultOpen?: boolean
  /** Force the open state (search auto-expands every group with results). */
  open?: boolean
  /**
   * Counter text override. Defaults to the i18n unit
   * `settings.agentPage.categoryCount` ("{{active}} de {{total}}"); pass it
   * when the caller already composed the label.
   */
  countLabel?: string
  children: ReactNode
  icon?: ReactNode
}

const HEADER_CLS =
  'flex w-full items-center gap-2 rounded-md px-1 hover:bg-background-tertiary transition-colors'

const TITLE_CLS = 'text-xs font-semibold uppercase tracking-wider text-text-tertiary'
const COUNT_CLS = 'text-[11px] text-text-muted'

export function CategoryGroup({
  title,
  activeCount,
  totalCount,
  defaultOpen = true,
  open,
  countLabel,
  children,
  icon,
}: Props) {
  const { t } = useTranslation()
  const panelId = `category-panel-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  const [collapsed, setCollapsed] = useState(!defaultOpen)

  // `open` (when provided) wins over local state; it is expressed inverted so
  // the internal state keeps meaning "collapsed".
  const isExpanded = open !== undefined ? open : !collapsed

  const counterText =
    countLabel ??
    t('settings.agentPage.categoryCount', {
      active: activeCount,
      total: totalCount,
      defaultValue: '{{active}} de {{total}}',
    })

  const toggle = () => setCollapsed((previous) => !previous)

  return (
    <section className="border-b border-border-light last:border-b-0">
      <button
        type="button"
        aria-expanded={isExpanded}
        aria-controls={panelId}
        onClick={toggle}
        className={`${HEADER_CLS} h-9`}
      >
        {icon ? (
          <span aria-hidden="true" className="flex flex-none items-center opacity-80">
            {icon}
          </span>
        ) : null}
        <span className={TITLE_CLS}>{title}</span>
        {/* Spec §7.4: the counter is shown wrapped in parentheses. */}
        <span className={`${COUNT_CLS} ml-auto whitespace-nowrap`}>({counterText})</span>
        <svg
          aria-hidden="true"
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className={`flex-none text-text-tertiary transition-transform duration-150 ${
            isExpanded ? 'rotate-90' : ''
          }`}
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </button>

      <div
        id={panelId}
        className={`grid transition-all duration-200 ${isExpanded ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]'}`}
      >
        <div className="overflow-hidden">
          <div className="pb-3 pt-1">{children}</div>
        </div>
      </div>
    </section>
  )
}
