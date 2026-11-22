import type { SlashCommandInfo } from '../../lib/types'

export function isPaletteTrigger(draft: string): boolean {
  if (!draft.startsWith('/')) return false
  return !/\s/.test(draft)
}

export function filterCommands(commands: SlashCommandInfo[], query: string): SlashCommandInfo[] {
  if (!isPaletteTrigger(query)) return []
  const needle = query.toLowerCase()
  return commands.filter((command) => command.name.toLowerCase().startsWith(needle))
}

export function completeDraft(_draft: string, command: SlashCommandInfo): string {
  return `${command.name} `
}
