export type ToolCategory = 'files' | 'system' | 'web' | 'agents' | 'hardware' | 'other'

export const CATEGORY_ORDER: ToolCategory[] = [
  'files',
  'system',
  'web',
  'agents',
  'hardware',
  'other',
]

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
    'update_memory',
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
  other: ['secret'],
}

const CATEGORY_BY_TOOL: Map<string, ToolCategory> = new Map(
  Object.entries(TOOL_CATEGORIES).flatMap(([category, tools]) =>
    tools.map((tool) => [tool, category as ToolCategory]),
  ),
)

export function categorize(toolName: string): ToolCategory {
  return CATEGORY_BY_TOOL.get(toolName) ?? 'other'
}

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

export const KNOWN_TOOLS: string[] = CATEGORY_ORDER.flatMap((category) => TOOL_CATEGORIES[category])

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
