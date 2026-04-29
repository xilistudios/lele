export const formatSessionTitle = (
  sessionKey: string,
  sessionName?: string,
  messageCount?: number,
) => {
  if (sessionName?.trim()) return sessionName
  if (!messageCount || messageCount === 0) return 'New Chat'
  const parts = sessionKey.split(':')
  return parts.length > 2 ? `Session ${parts[parts.length - 1]}` : sessionKey
}

// Maps internal provider names to user-friendly display names
export const PROVIDER_DISPLAY_NAMES: Record<string, string> = {
  alibaba_coding_plan: 'Alibaba Coding Plan',
  anthropic: 'Anthropic',
  deepseek: 'DeepSeek',
  discord: 'Discord',
  gemini: 'Google Gemini',
  github_copilot: 'GitHub Copilot',
  groq: 'Groq',
  modelark_coding_plan: 'ModelArk Coding Plan',
  moonshot: 'Moonshot',
  nanogpt: 'NanoGPT',
  nvidia: 'NVIDIA',
  ollama: 'Ollama',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
  shengsuanyun: 'ShengSuan Yun',
  vllm: 'vLLM',
  zai_coding_plan: 'zAI Coding Plan',
  zhipu: 'Zhipu',
}

// Converts a provider name to a user-friendly display name
export function getProviderDisplayName(name: string): string {
  return (
    PROVIDER_DISPLAY_NAMES[name] ||
    name
      .split('_')
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
      .join(' ')
  )
}
