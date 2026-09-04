import type { SlashCommandInfo } from '../../lib/types'

/**
 * Pure logic behind the composer's "/" slash-command palette.
 *
 * Kept dependency-free (and DOM-free) so the trigger/filter/complete rules can
 * be unit tested on their own: the palette is a composing aid only — the actual
 * command runs on the backend when the finished text is sent as a normal chat
 * message, so nothing here may try to execute anything.
 */

/**
 * True when the draft should open the palette: it starts with "/" and no
 * whitespace has been typed yet.
 *
 * "/cl" -> true (partial name), "/clear" -> true (exact match still shown so
 * Enter can accept it), "/clear x" -> false (a command is already in use with
 * args), "/ " -> false (a bare trigger plus whitespace is not a command).
 */
export function isPaletteTrigger(draft: string): boolean {
  if (!draft.startsWith('/')) return false
  return !/\s/.test(draft)
}

/**
 * Commands matching the draft, case-insensitively, by prefix on the name.
 *
 * Returns [] when the draft is not a palette trigger, so callers can pass the
 * raw draft. The backend list is already sorted; filtering preserves that order.
 */
export function filterCommands(commands: SlashCommandInfo[], query: string): SlashCommandInfo[] {
  if (!isPaletteTrigger(query)) return []
  const needle = query.toLowerCase()
  return commands.filter((command) => command.name.toLowerCase().startsWith(needle))
}

/**
 * Draft to install when the user accepts `command`.
 *
 * Always "<name> " with one trailing space: the space both hides the palette
 * (see isPaletteTrigger) and leaves the caret ready for arguments.
 */
export function completeDraft(_draft: string, command: SlashCommandInfo): string {
  return `${command.name} `
}
