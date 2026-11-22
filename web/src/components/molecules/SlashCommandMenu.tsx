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

function CommandRow({ command, active, rowId, onSelect, onHover, index }: RowProps) {
  return (
    // biome-ignore lint/a11y/useFocusableInteractive: rows must never take focus from the textarea (aria-activedescendant pattern); the composer drives the keyboard
    <div
      id={rowId}
      // biome-ignore lint/a11y/useSemanticElements: option rows belong to the parent listbox; <option> is valid only inside a native select, which cannot render styled name+description rows
      role="option"
      aria-selected={active}
      onMouseDown={(event) => {
        event.preventDefault()
        onSelect(command)
      }}
      onMouseEnter={() => onHover?.(index)}
      className={`flex cursor-pointer items-baseline gap-2 rounded-md px-2 py-1.5 text-xs transition-colors ${
        active ? 'bg-background-tertiary text-accent-primary' : 'text-text-primary'
      }`}
    >
      <code className="flex-shrink-0 font-mono text-xs font-semibold">{command.name}</code>
      <span className="min-w-0 truncate text-xs text-text-tertiary">{command.description}</span>
      {command.source ? (
        <span
          data-testid={`slash-command-source-${command.name}`}
          className="ml-auto flex-shrink-0 rounded-md border border-border bg-background-tertiary px-1 py-0.5 text-2xs font-medium uppercase text-text-tertiary"
        >
          {command.source}
        </span>
      ) : null}
    </div>
  )
}

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
      tabIndex={-1}
      className="mx-2 mb-1 max-h-48 max-md:max-h-[min(12rem,25dvh)] overflow-y-auto rounded-lg border border-border bg-background-secondary shadow-lg"
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
