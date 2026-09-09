/**
 * Tool catalog grouping for the agent "Tools" tab (spec §7.5).
 *
 * The backend exposes a flat tool list (`GET /api/v1/tools`) with no category
 * information, so this grouping is 100% presentation. Names are the real ones
 * registered in `pkg/tools/`.
 *
 * Fallback rule: a tool that is not listed here lands in `other` — unknown or
 * newly added tools are never lost from the UI.
 */

export type ToolCategory = 'files' | 'system' | 'web' | 'agents' | 'hardware' | 'other'

/** Display order of the category groups in the Tools tab. */
export const CATEGORY_ORDER: ToolCategory[] = [
  'files',
  'system',
  'web',
  'agents',
  'hardware',
  'other',
]

/** i18n key for a category label (namespace `settings.agentPage`). */
export const CATEGORY_LABEL_KEYS: Record<ToolCategory, string> = {
  files: 'settings.agentPage.category.files',
  system: 'settings.agentPage.category.system',
  web: 'settings.agentPage.category.web',
  agents: 'settings.agentPage.category.agents',
  hardware: 'settings.agentPage.category.hardware',
  other: 'settings.agentPage.category.other',
}

export const TOOL_CATEGORIES: Record<ToolCategory, string[]> = {
  files: [
    'read_file',
    'write_file',
    'edit_file',
    'append_file',
    'smart_edit',
    'patch',
    'sequential_replace',
    'list_dir',
    'read_image',
    'send_file',
  ],
  system: [
    'exec',
    'sleep',
    'list_background_execs',
    'get_background_exec_output',
    'stop_background_exec',
  ],
  web: ['web_search', 'web_fetch'],
  agents: [
    'spawn',
    'subagent',
    'list_active_subagents',
    'wait_for_subagent',
    'cancel_subagent',
    'cron',
    'group_chat',
    'message',
  ],
  hardware: ['i2c', 'spi'],
  // `secret` has no natural home among the functional groups; anything the
  // backend adds later also lands here until it is classified.
  other: ['secret'],
}

/** tool name -> category, derived once from TOOL_CATEGORIES. */
const CATEGORY_BY_TOOL: Map<string, ToolCategory> = new Map(
  Object.entries(TOOL_CATEGORIES).flatMap(([category, tools]) =>
    tools.map((tool) => [tool, category as ToolCategory]),
  ),
)

/** Category of a tool name; unknown names fall back to `other`. */
export function categorize(toolName: string): ToolCategory {
  return CATEGORY_BY_TOOL.get(toolName) ?? 'other'
}

/**
 * Minimum autonomous operating set: read/write/edit files, list a directory,
 * run commands, search and fetch the web, deliver a file, wait. Offered as the
 * "Essentials" batch button in the Tools tab (§4.6.3).
 */
export const ESSENTIAL_TOOLS: string[] = [
  'read_file',
  'write_file',
  'edit_file',
  'list_dir',
  'exec',
  'web_search',
  'web_fetch',
  'send_file',
  'sleep',
]

/** Every known tool name (catalog of the static map, without the fallback bucket). */
export const KNOWN_TOOLS: string[] = CATEGORY_ORDER.flatMap((category) => TOOL_CATEGORIES[category])

/** Group a list of tool names by category, in CATEGORY_ORDER, dropping empty groups. */
export function groupToolsByCategory(tools: string[]): Array<{
  category: ToolCategory
  tools: string[]
}> {
  const buckets = new Map<ToolCategory, string[]>()
  for (const tool of tools) {
    const category = categorize(tool)
    const bucket = buckets.get(category)
    if (bucket) bucket.push(tool)
    else buckets.set(category, [tool])
  }
  return CATEGORY_ORDER.filter((category) => buckets.has(category)).map((category) => ({
    category,
    tools: buckets.get(category) as string[],
  }))
}
