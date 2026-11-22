export const COMMAND_FRONTMATTER_KEYS = [
  'description',
  'agent',
  'model',
  'allow_shell',
  'allow_absolute_files',
] as const

export type CommandFrontmatterKey = (typeof COMMAND_FRONTMATTER_KEYS)[number]

export function isCommandFrontmatterKey(key: string): key is CommandFrontmatterKey {
  return (COMMAND_FRONTMATTER_KEYS as readonly string[]).includes(key)
}

export type TriState = boolean | null

export type CommandFields = {
  description: string
  agent: string
  model: string
  allow_shell: boolean
  allow_absolute_files: TriState
}

export function emptyCommandFields(): CommandFields {
  return {
    description: '',
    agent: '',
    model: '',
    allow_shell: false,
    allow_absolute_files: null,
  }
}

export const COMMAND_NAME_RE = /^[a-z0-9][a-z0-9._-]*$/

export const COMMAND_NAME_MAX_LEN = 64

export const COMMAND_RESERVED_EXTS = ['.md', '.markdown'] as const

export function isValidCommandName(name: string): boolean {
  return COMMAND_NAME_RE.test(name)
}

export function commandNameError(name: string): CommandNameError | null {
  const trimmed = name.trim()
  if (trimmed.length > COMMAND_NAME_MAX_LEN) return 'tooLong'
  if (!COMMAND_NAME_RE.test(trimmed)) return 'invalid'
  if (COMMAND_RESERVED_EXTS.some((ext) => trimmed.endsWith(ext))) return 'reservedExt'
  return null
}

export type CommandNameError = 'invalid' | 'tooLong' | 'reservedExt'

function fieldValue(value: string): string {
  return `"${value.replace(/[\r\n]+/g, ' ')}"`
}

export function serializeCommandMarkdown(fields: CommandFields, body: string): string {
  const lines: string[] = []
  if (fields.description) lines.push(`description: ${fieldValue(fields.description)}`)
  if (fields.agent) lines.push(`agent: ${fieldValue(fields.agent)}`)
  if (fields.model) lines.push(`model: ${fieldValue(fields.model)}`)
  if (fields.allow_shell) lines.push('allow_shell: true')
  if (fields.allow_absolute_files !== null) {
    lines.push(`allow_absolute_files: ${fields.allow_absolute_files ? 'true' : 'false'}`)
  }
  return `---\n${lines.join('\n')}\n---\n${body}`
}

export type ParsedCommandMarkdown = {
  fields: CommandFields
  body: string
  hasFrontmatter: boolean
}

function unquote(value: string): string {
  if (value.length >= 2) {
    const first = value[0]
    const last = value[value.length - 1]
    if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
      return value.slice(1, -1)
    }
  }
  return value
}

function parseBool(value: string): boolean | null {
  const lower = value.toLowerCase()
  if (['1', 't', 'true', 'yes', 'on'].includes(lower)) return true
  if (['0', 'f', 'false', 'no', 'off'].includes(lower)) return false
  return null
}

export function parseCommandMarkdown(content: string): ParsedCommandMarkdown {
  const fields: CommandFields = emptyCommandFields()
  const match = /^---\r?\n([\s\S]*?)\r?\n---(\r?\n|$)/.exec(content)
  if (!match) {
    return { fields, body: content.trim(), hasFrontmatter: false }
  }
  for (const rawLine of match[1].split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue
    const separator = line.indexOf(':')
    if (separator < 0) continue
    const key = line.slice(0, separator).trim().toLowerCase()
    if (!isCommandFrontmatterKey(key)) continue
    const value = unquote(line.slice(separator + 1).trim())
    switch (key) {
      case 'description':
        fields.description = value
        break
      case 'agent':
        fields.agent = value
        break
      case 'model':
        fields.model = value
        break
      case 'allow_shell': {
        const parsed = parseBool(value)
        if (parsed !== null) fields.allow_shell = parsed
        break
      }
      case 'allow_absolute_files': {
        const parsed = parseBool(value)
        if (parsed !== null) fields.allow_absolute_files = parsed
        break
      }
      default:
        break
    }
  }
  return { fields, body: content.slice(match[0].length).trim(), hasFrontmatter: true }
}
