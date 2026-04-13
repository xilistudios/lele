export function isDirtyPath(dirtyPaths: Set<string>, path: string): boolean {
  return dirtyPaths.has(path) || dirtyPaths.has(path.split('.').slice(0, -1).join('.'))
}

export function getErrorForPath(
  errors: Array<{ path: string; message: string }>,
  path: string,
): string | undefined {
  const error = errors.find((e) => e.path === path || e.path.startsWith(`${path}.`))
  return error?.message
}

export function getAgentModelPrimary(
  agentModel: string | { primary?: string; fallbacks?: string[] } | undefined,
): string {
  if (!agentModel) return ''
  if (typeof agentModel === 'string') return agentModel
  return agentModel.primary || ''
}

export function getDefaultModel(draftConfig: unknown): string {
  const config = draftConfig as { agents?: { defaults?: { model?: string } } } | null
  return config?.agents?.defaults?.model || ''
}

export function getDefaultImageModel(draftConfig: unknown): string {
  const config = draftConfig as { agents?: { defaults?: { image_model?: string } } } | null
  return config?.agents?.defaults?.image_model || ''
}
