export const KEY_PREFIXES: ReadonlyArray<{ prefix: string; type: string }> = [
  { prefix: 'sk-ant-', type: 'anthropic' },
  { prefix: 'sk-or-v1-', type: 'openrouter' },
  { prefix: 'sk-or-', type: 'openrouter' },
  { prefix: 'sk-proj-', type: 'openai' },
  { prefix: 'sk-svcacct-', type: 'openai' },
  { prefix: 'gsk_', type: 'groq' },
  { prefix: 'AIza', type: 'gemini' },
  { prefix: 'xai-', type: 'xai' },
  { prefix: 'nvapi-', type: 'nvidia' },
  { prefix: 'pplx-', type: 'perplexity' },
]

export function detectProviderFromKey(key: string): string | null {
  const trimmed = key.trim()
  for (const { prefix, type } of KEY_PREFIXES) {
    if (trimmed.startsWith(prefix)) return type
  }
  return null
}
