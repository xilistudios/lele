import { useTranslation } from 'react-i18next'
import type { ToolMessageStatus } from '../../lib/types'

type IconConfig = { icon: string; color: string }

const FILE_OPS = new Set([
  'read_file',
  'write_file',
  'edit_file',
  'smart_edit',
  'patch',
  'sequential_replace',
  'append_file',
  'list_dir',
])

// Map tool names to icon configs only (labels come from i18n)
const TOOL_ICONS: Record<string, IconConfig> = {
  read_file: {
    icon: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z M14 2v6h6 M16 13H8 M16 17H8 M10 9H8',
    color: 'text-blue-400',
  },
  write_file: {
    icon: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z M14 2v6h6 M12 18v-6 M9 15l3 3 3-3',
    color: 'text-green-400',
  },
  edit_file: {
    icon: 'M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7 M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z',
    color: 'text-yellow-400',
  },
  smart_edit: {
    icon: 'M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7 M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z',
    color: 'text-yellow-400',
  },
  patch: {
    icon: 'M12 2v4 M12 18v4 M4.93 4.93l2.83 2.83 M16.24 16.24l2.83 2.83 M2 12h4 M18 12h4 M4.93 19.07l2.83-2.83 M16.24 7.76l2.83-2.83',
    color: 'text-orange-400',
  },
  sequential_replace: {
    icon: 'M12 2v4 M12 18v4 M4.93 4.93l2.83 2.83 M16.24 16.24l2.83 2.83 M2 12h4 M18 12h4 M4.93 19.07l2.83-2.83 M16.24 7.76l2.83-2.83',
    color: 'text-orange-400',
  },
  append_file: {
    icon: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z M14 2v6h6 M12 18v-6 M9 15l3 3 3-3',
    color: 'text-emerald-400',
  },
  list_dir: {
    icon: 'M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z',
    color: 'text-cyan-400',
  },
  exec: {
    icon: 'M4 17l6-6-6-6 M12 19h8',
    color: 'text-purple-400',
  },
  web_search: {
    icon: 'M21 12a9 9 0 1 1-6.219-8.56 M21 12c0 1.66-4 3-9 3s-9-1.34-9-3 M3 12v-3c0-1.66 4-3 9-3s9 1.34 9 3v3',
    color: 'text-sky-400',
  },
  web_fetch: {
    icon: 'M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 17.93c-3.95-.49-7-3.85-7-7.93 0-.62.08-1.21.21-1.79L9 15v1c0 1.1.9 2 2 2v1.93zm6.9-2.54c-.26-.81-1-1.39-1.9-1.39h-1v-3c0-.55-.45-1-1-1H8v-2h2c.55 0 1-.45 1-1V7h2c1.1 0 2-.9 2-2v-.41c2.93 1.19 5 4.06 5 7.41 0 2.08-.8 3.97-2.1 5.39z',
    color: 'text-indigo-400',
  },
  cron: {
    icon: 'M12 8v4l3 3m6-3a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
    color: 'text-pink-400',
  },
  spawn: {
    icon: 'M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5',
    color: 'text-amber-400',
  },
  i2c: {
    icon: 'M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8z M12 6v6l4 2',
    color: 'text-rose-400',
  },
  spi: {
    icon: 'M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8z M8 12h8',
    color: 'text-fuchsia-400',
  },
}

const GENERIC_ICON = 'M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2 2 0 0 1-2.83 0a2 2 0 0 1 0-2.83l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z'

function getIconConfig(toolName?: string): IconConfig {
  if (!toolName) return { icon: GENERIC_ICON, color: 'text-text-tertiary' }

  const key = toolName.toLowerCase()
  const config = TOOL_ICONS[key]
  if (config) return config

  const baseName = key.split('_')[0]
  const baseConfig = TOOL_ICONS[baseName]
  if (baseConfig) return baseConfig

  return { icon: GENERIC_ICON, color: 'text-text-tertiary' }
}

function getToolLabel(toolName: string | undefined, t: (key: string) => string): string {
  if (!toolName) return t('toolCalls.genericAction')

  const labels: Record<string, string> = {
    read_file: t('toolCalls.readFile'),
    write_file: t('toolCalls.writeFile'),
    edit_file: t('toolCalls.editFile'),
    smart_edit: t('toolCalls.editFile'),
    patch: t('toolCalls.applyPatch'),
    sequential_replace: t('toolCalls.replaceText'),
    append_file: t('toolCalls.appendFile'),
    list_dir: t('toolCalls.listDir'),
    exec: t('toolCalls.execCommand'),
    web_search: t('toolCalls.webSearch'),
    web_fetch: t('toolCalls.webFetch'),
    cron: t('toolCalls.cronTask'),
    spawn: t('toolCalls.spawnSubagent'),
    i2c: t('toolCalls.i2c'),
    spi: t('toolCalls.spi'),
  }

  const key = toolName.toLowerCase()
  return labels[key] || t('toolCalls.genericAction')
}

function parseArgsSummary(toolName?: string, args?: string): string {
  if (!args) return ''

  const lines = args.split('\n')
  let firstLine = lines[0].trim()

  // Handle "toolName {...}" format: extract the JSON part
  const jsonStart = firstLine.indexOf('{')
  if (jsonStart !== -1) {
    try {
      const jsonStr = firstLine.slice(jsonStart)
      const parsed = JSON.parse(jsonStr)
      if (parsed.path) return parsed.path
      if (parsed.url) return parsed.url
      if (parsed.command) {
        const cmd = parsed.command
        if (cmd.length > 60) return `${cmd.slice(0, 57)}...`
        return cmd
      }
      if (parsed.query) return parsed.query
      if (parsed.message) return parsed.message
    } catch {
      // Not valid JSON, fall through
    }
  }

  if (firstLine.length > 50) return `${firstLine.slice(0, 47)}...`
  return firstLine
}

type Props = {
  toolName?: string
  toolArgs?: string
  toolResult?: string
  toolStatus?: ToolMessageStatus
  subagentSessionKey?: string
  onNavigateToSession?: (sessionKey: string) => void
  expanded?: boolean
  onToggleExpand?: () => void
}

export function ToolCallDisplay({
  toolName,
  toolArgs,
  toolResult,
  toolStatus,
  subagentSessionKey,
  onNavigateToSession,
  expanded = false,
  onToggleExpand,
}: Props) {
  const { t } = useTranslation()
  const iconConfig = getIconConfig(toolName)
  const label = getToolLabel(toolName, t)
  const argsSummary = parseArgsSummary(toolName, toolArgs)
  const isFileOp = toolName ? FILE_OPS.has(toolName.toLowerCase()) : false

  // For file ops: show path only, no expansion
  if (isFileOp) {
    return (
      <div className="group">
        <div className="rounded-lg border border-border bg-background-secondary/50 overflow-hidden">
          <div className="flex w-full items-center gap-2 px-3 py-1.5 text-left">
            {/* Tool icon */}
            <div className={`flex-shrink-0 rounded-md p-1 ${iconConfig.color}`}>
              <svg
                className="h-4 w-4"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                aria-hidden="true"
              >
                {iconConfig.icon
                  .split(' M')
                  .map((path, i) =>
                    i === 0 ? <path key={i} d={path} /> : <path key={i} d={`M${path}`} />,
                  )}
              </svg>
            </div>

            {/* Label + path summary */}
            {argsSummary ? (
              <span className="min-w-0 flex items-center gap-1 text-xs">
                <span className="text-text-secondary">{label}</span>
                <span className="text-text-tertiary font-mono truncate">: {argsSummary}</span>
              </span>
            ) : (
              <span className="text-sm text-text-secondary">{label}</span>
            )}

            {/* Error badge */}
            {toolStatus === 'error' && (
              <span className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium bg-red-500/10 text-red-400 border border-red-500/20 ml-auto">
                {t('toolCalls.error')}
              </span>
            )}
          </div>
        </div>
      </div>
    )
  }

  // For non-file ops, show result when expanded
  const showResult = toolResult

  return (
    <div className="group">
      {/* Header — same style as thinking block */}
      <div
        className={`rounded-lg border border-border bg-background-secondary/50 overflow-hidden transition-colors ${
          expanded ? '' : 'hover:bg-background-secondary'
        }`}
      >
        <button
          type="button"
          className="flex w-full items-center gap-2 px-3 py-1.5 text-left"
          aria-expanded={expanded}
          onClick={onToggleExpand}
        >
          {/* Tool icon */}
          <div className={`flex-shrink-0 rounded-md p-1 ${iconConfig.color}`}>
            <svg
              className="h-4 w-4"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              aria-hidden="true"
            >
              {iconConfig.icon
                .split(' M')
                .map((path, i) =>
                  i === 0 ? <path key={i} d={path} /> : <path key={i} d={`M${path}`} />,
                )}
            </svg>
          </div>

          {/* Label */}
          <span className="text-sm text-text-secondary">{label}</span>

          {/* Summary: command for exec, path for files, etc. */}
          {argsSummary && (
            <span className="min-w-0 truncate text-xs text-text-tertiary font-mono">{argsSummary}</span>
          )}

          {/* Error badge only */}
          {toolStatus === 'error' && (
            <span className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium bg-red-500/10 text-red-400 border border-red-500/20">
              {t('toolCalls.error')}
            </span>
          )}

          {/* Subagent nav */}
          {subagentSessionKey && toolStatus !== 'executing' && onNavigateToSession && (
            <button
              type="button"
              aria-label={t('toolCalls.openSubagent')}
              className="ml-auto p-0.5 rounded-md hover:bg-background-secondary transition-colors"
              onClick={(e) => {
                e.stopPropagation()
                onNavigateToSession(subagentSessionKey)
              }}
            >
              <svg
                className="h-3.5 w-3.5 text-text-tertiary hover:text-text-primary"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <path d="M5 12h14" />
                <path d="m12 5 7 7-7 7" />
              </svg>
            </button>
          )}

          {/* Chevron */}
          <svg
            className={`h-3 w-3 text-text-tertiary transition-transform ml-auto ${expanded ? 'rotate-90' : ''}`}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
        </button>

        {/* Expanded: result only for non-file ops */}
        {expanded && showResult && (
          <div className="px-3 pb-2">
            <pre className="text-xs text-text-tertiary font-mono whitespace-pre-wrap overflow-x-auto max-h-[300px]">
              {toolResult}
            </pre>
          </div>
        )}
      </div>
    </div>
  )
}
