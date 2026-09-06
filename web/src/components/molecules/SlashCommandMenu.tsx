import { useTranslation } from 'react-i18next'
import type { SlashCommandInfo } from '../../lib/types'

type Props = {
  items: SlashCommandInfo[]
  activeIndex: number
  onSelect: (command: SlashCommandInfo) => void
  onHover?: (index: number) => void
}

type RowProps = {
  command: SlashCommandInfo
  active: boolean
  rowId: string
  onSelect: (command: SlashCommandInfo) => void
  onHover?: (index: number) => void
  index: number
}

/**
 * One palette row. Split out of the list only so the ARIA suppressions below
 * attach to the element (biome 1.x needs them directly above the returned JSX).
 */
function CommandRow({ command, active, rowId, onSelect, onHover, index }: RowProps) {
  return (
    // biome-ignore lint/a11y/useFocusableInteractive: rows must never take focus from the textarea (aria-activedescendant pattern); the composer drives the keyboard
    <div
      id={rowId}
      // biome-ignore lint/a11y/useSemanticElements: option rows belong to the parent listbox; <option> is valid only inside a native select, which cannot render styled name+description rows
      role="option"
      aria-selected={active}
      // mousedown, not click: clicking must not blur the textarea first.
      onMouseDown={(event) => {
        event.preventDefault()
        onSelect(command)
      }}
      onMouseEnter={() => onHover?.(index)}
      className={`flex cursor-pointer items-baseline gap-2 rounded-md px-2 py-1.5 text-xs transition-colors ${
        active ? 'bg-background-tertiary text-accent-primary' : 'text-text-primary'
      }`}
    >
      <code className="flex-shrink-0 font-mono text-[11px] font-semibold">{command.name}</code>
      <span className="min-w-0 truncate text-[11px] text-text-tertiary">{command.description}</span>
      {/* Custom (harness-defined) commands carry `source`; built-ins do not. */}
      {command.source ? (
        <span
          data-testid={`slash-command-source-${command.name}`}
          className="ml-auto flex-shrink-0 rounded-md border border-border bg-background-tertiary px-1 py-0.5 text-[9px] font-medium uppercase text-text-tertiary"
        >
          {command.source}
        </span>
      ) : null}
    </div>
  )
}

/**
 * Popup list shown above the composer textarea while the draft is a "/" trigger.
 *
 * Display-only regarding focus: the textarea keeps the caret so typing continues
 * to filter, and the menu is never in the tab order (tabIndex={-1} only makes it
 * addressable for aria-activedescendant). Keyboard navigation is driven by the
 * composer (ArrowUp/Down/Tab/Enter) and reflected through `activeIndex`; mouse
 * users get hover + click, mirroring the TUI palette.
 *
 * Descriptions come from the backend in English and are rendered verbatim:
 * custom commands will not have i18n keys, so translating them client-side is
 * not an option.
 */
export function SlashCommandMenu({ items, activeIndex, onSelect, onHover }: Props) {
  const { t } = useTranslation()
  const hint = t('chat.commandsHint')

  return (
    // biome-ignore lint/a11y/useSemanticElements: a native select cannot render the styled two-column rows (name + description) the palette needs, and the ARIA listbox pattern keeps focus in the composer textarea
    <div
      role="listbox"
      id="slash-command-menu"
      aria-label={hint}
      title={hint}
      // Focus (and aria-activedescendant) stays in the textarea; -1 keeps the
      // list out of the tab order.
      tabIndex={-1}
      className="mx-2 mb-1 max-h-48 overflow-y-auto rounded-lg border border-border bg-background-secondary shadow-lg"
      data-testid="slash-command-menu"
    >
      {items.map((command, index) => (
        <CommandRow
          key={command.name}
          command={command}
          index={index}
          rowId={`slash-command-${index}`}
          active={index === activeIndex}
          onSelect={onSelect}
          onHover={onHover}
        />
      ))}
    </div>
  )
}
