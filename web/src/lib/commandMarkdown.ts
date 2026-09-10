/**
 * Command markdown frontmatter (per-agent Commands tab, brief §6 decision 9).
 *
 * A custom slash command is a markdown file: an optional YAML-ish frontmatter
 * block (`---\nkey: value\n---`) followed by the prompt template. The single
 * source of truth for this format is the Go parser (`pkg/harness/loader.go`),
 * which only understands `description`, `agent`, `model`, `allow_shell` and
 * `allow_absolute_files` and IGNORES unknown keys in silence. The wire
 * contract decided that serialisation lives HERE (TypeScript) and validation
 * lives THERE (Go): the client composes text, the server parses it.
 *
 * This module is pure — no React, no i18n — so both the editor dialog and the
 * tests can rely on it without rendering anything.
 */

/** The five keys the harness parser knows; anything else is dropped on write. */
export const COMMAND_FRONTMATTER_KEYS = [
  'description',
  'agent',
  'model',
  'allow_shell',
  'allow_absolute_files',
] as const

export type CommandFrontmatterKey = (typeof COMMAND_FRONTMATTER_KEYS)[number]

/** Type guard for the keys the Go parser understands. */
export function isCommandFrontmatterKey(key: string): key is CommandFrontmatterKey {
  return (COMMAND_FRONTMATTER_KEYS as readonly string[]).includes(key)
}

/** Tri-state value: `null` = inherit the harness default (omit the key). */
export type TriState = boolean | null

/** Structured frontmatter the editor works with. */
export type CommandFields = {
  description: string
  agent: string
  model: string
  allow_shell: boolean
  /** null = inherit → the key is OMITTED from the serialized frontmatter. */
  allow_absolute_files: TriState
}

/** The default an editor starts from: only description is meaningful. */
export function emptyCommandFields(): CommandFields {
  return {
    description: '',
    agent: '',
    model: '',
    allow_shell: false,
    allow_absolute_files: null,
  }
}

/**
 * The default name regex the backend enforces (`^[a-z0-9][a-z0-9._-]*$`):
 * lowercase alphanumeric start, then lowercase alphanumerics, dots, dashes and
 * underscores. Exported so the dialog and the tests share ONE definition.
 */
export const COMMAND_NAME_RE = /^[a-z0-9][a-z0-9._-]*$/

/** The name becomes a file stem, and every filesystem has a bound. Mirrors
 *  `maxAgentCommandNameLen` in pkg/channels/rest_agent_commands.go. */
export const COMMAND_NAME_MAX_LEN = 64

/** Stems that would double up once ".md" is appended ("review.md" → the loader
 *  reports a command called "review.md"). Mirrors `agentCommandReservedExts`. */
export const COMMAND_RESERVED_EXTS = ['.md', '.markdown'] as const

/** True when `name` matches the name grammar alone. Use `commandNameError` for
 *  anything the user is about to submit: the server checks three rules. */
export function isValidCommandName(name: string): boolean {
  return COMMAND_NAME_RE.test(name)
}

/**
 * Why the backend would reject this name, or null when it would accept it.
 *
 * The server validates the name on create AND on every path segment, so the
 * form must predict the same verdict: a field that looks fine and then comes
 * back with an HTTP 400 the label cannot explain is worse than one that blocks
 * the Save button. Kept as one function so the rules cannot drift apart from
 * the error message shown for them.
 */
export function commandNameError(name: string): CommandNameError | null {
  const trimmed = name.trim()
  // Same order as agentCommandName() in Go, so the message the form shows is
  // the one the server would have produced.
  if (trimmed.length > COMMAND_NAME_MAX_LEN) return 'tooLong'
  if (!COMMAND_NAME_RE.test(trimmed)) return 'invalid'
  if (COMMAND_RESERVED_EXTS.some((ext) => trimmed.endsWith(ext))) return 'reservedExt'
  return null
}

/** Which of the server's name rules failed. */
export type CommandNameError = 'invalid' | 'tooLong' | 'reservedExt'

/**
 * Emit one frontmatter value.
 *
 * Always wrapped in double quotes, NEVER escaped. Go's `unquote
 * (pkg/harness/loader.go)` strips exactly one pair of outer quotes and leaves
 * everything inside alone, so `"` + value + `"` round-trips ANY single-line
 * text: `say "hi"` → `"say "hi""` → back to `say "hi"`. Escaping would be
 * worse than useless — the backslashes would reach the agent verbatim, because
 * nothing unescapes them. Verified against the real Go parser for values that
 * are empty, one quote, quote-wrapped, backslash-terminated, colon-bearing and
 * space-padded.
 *
 * The one thing the format cannot store is a line break (the parser reads one
 * `key: value` per line), so those are flattened to spaces. The dialog uses
 * single-line inputs for every field this writes, so it is a safety net, not
 * the normal path.
 */
function fieldValue(value: string): string {
  return `"${value.replace(/[\r\n]+/g, ' ')}"`
}

/**
 * Serialize frontmatter + body into the full markdown file content.
 *
 * Rules (fixed so the Go parser always sees the shape it produces itself):
 * - key order is ALWAYS description, agent, model, allow_shell,
 *   allow_absolute_files;
 * - only keys with a value are written: `agent`/`model` are omitted when
 *   empty, `allow_shell` when false, `allow_absolute_files` when null
 *   ("inherit");
 * - strings are written verbatim (see `fieldValue`), booleans are `true`/`false`;
 * - a frontmatter block is ALWAYS emitted, even when every field is empty
 *   (honest and parseable);
 * - the body follows the closing `---` verbatim (no trimming, no re-wrapping).
 */
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

/** Result of parsing a command markdown file. */
export type ParsedCommandMarkdown = {
  /** Only the five known keys; unknown ones are DROPPED (documented choice:
   *  it mirrors the Go parser, which ignores them in silence — keeping them
   *  would advertise config the runtime never reads). */
  fields: CommandFields
  /** Template body after the closing `---` (trimmed, like the Go parser). */
  body: string
  /** True when a frontmatter block was found at the start of the file. */
  hasFrontmatter: boolean
}

/**
 * Strip one layer of surrounding single/double quotes — and NOTHING else.
 *
 * Byte-identical to Go's `unquote` (pkg/harness/loader.go), which slices the
 * outer pair without unescaping: there is no escape language in this format,
 * so `\"` stays `\"` and `\\` stays `\\`. Mirroring that exactly is what makes
 * a hand-written file and a file the editor wrote parse the same way; a TS
 * parser that unescaped would show the user text the agent never sees.
 */
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

/** Go's `strconv.ParseBool` accepts these; anything else keeps the current
 *  value (the real validator is the server, which answers 400). */
function parseBool(value: string): boolean | null {
  const lower = value.toLowerCase()
  if (['1', 't', 'true', 'yes', 'on'].includes(lower)) return true
  if (['0', 'f', 'false', 'no', 'off'].includes(lower)) return false
  return null
}

/**
 * Tolerant frontmatter parser for the editor.
 *
 * Deliberately NOT a YAML parser: the format is `key: value` lines between two
 * `---` fences. Behaviour mirrors `pkg/harness/loader.go` line for line:
 * - the block must start at byte 0 of the file;
 * - blank lines and `#` comments are skipped; a line without `:` is skipped;
 * - the LAST occurrence of a key wins: Go assigns the field on every matching
 *   line, so an earlier duplicate is overwritten. Matching that keeps the
 *   editor from showing a value the agent will not use;
 * - unknown keys are dropped (see `ParsedCommandMarkdown.fields`);
 * - an UNCLOSED block (`---` at the top, no second fence) yields no
 *   frontmatter: the whole file is the body, exactly like the Go regex, which
 *   simply does not match — the editor then shows an empty form over the raw
 *   text and saving re-wraps it. The server stays the validator.
 *
 * One divergence is accepted on purpose: Go ERRORS on an unparseable
 * `allow_shell` and the manager then skips the whole command, while this parser
 * keeps the default. The server re-validates on save (400 invalid_content), so
 * the file can never be written back broken — and reading it is still useful to
 * fix it.
 */
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
