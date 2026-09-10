import { useQuery } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../../../contexts/SettingsContext'
import { AddButton } from '../../atoms/AddButton'

const OPENAI_COMPATIBLE_TYPES = new Set([
  'openai',
  'gpt',
  'anthropic',
  'claude',
  'groq',
  'openrouter',
  'deepseek',
  'gemini',
  'google',
  'ollama',
  'ollama_cloud',
  'vllm',
  'nvidia',
  'nim',
  'moonshot',
  'kimi',
  'kimi_for_coding',
  'nanogpt',
  'chutes',
  'zhipu',
  'glm',
  'alibaba',
  'alibaba_coding_plan',
  'qwen',
  'qwen_portal',
  'shengsuanyun',
  'zai_coding_plan',
  'zai',
  'modelark_coding_plan',
  'modelark',
  'xai',
  'grok',
  'nous',
  'lmstudio',
  'lmstudio_local',
  'stepfun',
  'minimax',
  'minimax_cn',
  'vercel',
  'opencode',
  'opencode_go',
  'huggingface',
  'novita',
  'xiaomi',
  'tencent_tokenhub',
  'arcee',
  'gmi',
  'cerebras',
  'together',
  'fireworks',
  'mistral',
  'siliconflow',
  'perplexity',
  'github_copilot',
  'azure_foundry',
  'bedrock',
  'custom',
])

const PROVIDER_TYPE_ALIASES: Record<string, string> = {
  gpt: 'openai',
  claude: 'anthropic',
  'claude-code': 'anthropic',
  claudecode: 'anthropic',
  glm: 'zhipu',
  'z-ai': 'zhipu',
  'z.ai': 'zhipu',
  google: 'gemini',
  'google-gemini': 'gemini',
  kimi: 'moonshot',
  'kimi-coding': 'moonshot',
  'moonshot-ai': 'moonshot',
  nim: 'nvidia',
  'nvidia-nim': 'nvidia',
  copilot: 'github_copilot',
  github: 'github_copilot',
  'github-copilot': 'github_copilot',
  qwen: 'alibaba',
  dashscope: 'alibaba',
  aliyun: 'alibaba',
  'alibaba-cloud': 'alibaba',
  'alibaba-coding': 'alibaba_coding_plan',
  grok: 'xai',
  'x.ai': 'xai',
  'x-ai': 'xai',
  'lm-studio': 'lmstudio',
  lm_studio: 'lmstudio',
  step: 'stepfun',
  'minimax-china': 'minimax_cn',
  'ai-gateway': 'vercel',
  aigateway: 'vercel',
  'vercel-ai-gateway': 'vercel',
  zen: 'opencode',
  'opencode-zen': 'opencode',
  hf: 'huggingface',
  'hugging-face': 'huggingface',
  'novita-ai': 'novita',
  novitaai: 'novita',
  mimo: 'xiaomi',
  'xiaomi-mimo': 'xiaomi',
  tencent: 'tencent_tokenhub',
  tokenhub: 'tencent_tokenhub',
  'tencent-cloud': 'tencent_tokenhub',
  tencentmaas: 'tencent_tokenhub',
  'gmi-cloud': 'gmi',
  gmicloud: 'gmi',
  togetherai: 'together',
  'fireworks-ai': 'fireworks',
  'perplexity-agent': 'perplexity',
  'ollama-cloud': 'ollama_cloud',
  azure: 'azure_foundry',
  'azure-foundry': 'azure_foundry',
  aws: 'bedrock',
  'aws-bedrock': 'bedrock',
  amazon: 'bedrock',
  'amazon-bedrock': 'bedrock',
}

export function isOpenAICompatible(type: string | undefined): boolean {
  if (!type) return false
  return OPENAI_COMPATIBLE_TYPES.has(type)
}

export function normalizeProviderType(raw: string): string {
  const t = raw.toLowerCase().trim().replace(/\./g, '-').replace(/ /g, '_')
  return PROVIDER_TYPE_ALIASES[t] ?? t
}

export type ModelSuggestion = {
  id: string
  owned_by?: string
  context_window?: number
  max_output?: number
  vision?: boolean
  thinking_levels?: string[]
  reasoning?: boolean
}

export type AddModelPayload = {
  id: string
  context_window?: number
  max_output?: number
  vision?: boolean
  thinking_levels?: string[]
  reasoning?: boolean
}

const INPUT_CLS =
  'w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary focus:ring-offset-2 focus:ring-offset-background-primary disabled:opacity-40'

function formatContextWindow(n: number): string {
  if (n >= 1_000_000) {
    const m = n / 1_000_000
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`
  }
  if (n >= 1_000) {
    const k = n / 1_000
    return `${Number.isInteger(k) ? k : Math.round(k)}k`
  }
  return String(n)
}

type Props = {
  providerName: string
  providerType: string | undefined
  existingModels: string[]
  onAddModel: (model: AddModelPayload) => void
}

export function ModelSearchInput({
  providerName,
  providerType,
  existingModels,
  onAddModel,
}: Props) {
  const { t } = useTranslation()
  const { api } = useSettings()
  const [query, setQuery] = useState('')
  const [isOpen, setIsOpen] = useState(false)
  const [selectedIndex, setSelectedIndex] = useState(-1)
  const wrapperRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const searchPlaceholder = isOpenAICompatible(providerType)
  const catalogProvider = normalizeProviderType(providerType || providerName)

  const {
    data: models = [],
    isLoading,
    error,
  } = useQuery({
    queryKey: ['settingsModelSuggestions', providerName, providerType ?? ''],
    queryFn: async (): Promise<ModelSuggestion[]> => {
      // Prefer the live endpoint: backend merges catalog metadata and falls
      // back to catalog models when the provider has no usable key.
      try {
        const live = await api.providerModels(providerName)
        if (live.models?.length) return live.models
      } catch {
        // fall through to catalog
      }
      const catalog = await api.catalogModels(catalogProvider)
      return catalog.models ?? []
    },
    enabled: isOpen,
    staleTime: 60_000,
    retry: 1,
  })

  const existingSet = new Set(existingModels)

  const filtered = query.trim()
    ? models.filter((m) => m.id.toLowerCase().includes(query.toLowerCase().trim()))
    : models

  const selectable = filtered.filter((m) => !existingSet.has(m.id))

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setIsOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  const prevSelectableLenRef = useRef(selectable.length)
  if (selectable.length !== prevSelectableLenRef.current) {
    prevSelectableLenRef.current = selectable.length
    if (selectedIndex >= selectable.length) {
      setSelectedIndex(-1)
    }
  }

  useEffect(() => {
    if (selectedIndex >= 0 && listRef.current) {
      const items = listRef.current.children
      if (items[selectedIndex]) {
        items[selectedIndex].scrollIntoView({ block: 'nearest' })
      }
    }
  }, [selectedIndex])

  const handleSelect = useCallback(
    (model: ModelSuggestion) => {
      onAddModel({
        id: model.id,
        context_window: model.context_window,
        max_output: model.max_output,
        vision: model.vision,
        thinking_levels: model.thinking_levels,
        reasoning: model.reasoning,
      })
      setQuery('')
      setIsOpen(false)
      setSelectedIndex(-1)
      inputRef.current?.focus()
    },
    [onAddModel],
  )

  const handleAddManual = useCallback(() => {
    const key = query.trim()
    if (!key) return
    onAddModel({ id: key })
    setQuery('')
    setIsOpen(false)
  }, [query, onAddModel])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        setIsOpen(false)
        return
      }
      if (!isOpen || selectable.length === 0) {
        if (e.key === 'Enter') {
          e.preventDefault()
          handleAddManual()
        }
        return
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedIndex((i) => Math.min(i + 1, selectable.length - 1))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedIndex((i) => Math.max(i - 1, -1))
      } else if (e.key === 'Enter') {
        e.preventDefault()
        if (selectedIndex >= 0 && selectedIndex < selectable.length) {
          handleSelect(selectable[selectedIndex])
        } else {
          handleAddManual()
        }
      }
    },
    [isOpen, selectable, selectedIndex, handleSelect, handleAddManual],
  )

  return (
    <div className="flex gap-2" ref={wrapperRef}>
      <div className="relative flex-1">
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setSelectedIndex(-1)
            if (!isOpen) setIsOpen(true)
          }}
          onFocus={() => setIsOpen(true)}
          onKeyDown={handleKeyDown}
          placeholder={
            searchPlaceholder
              ? t('settings.searchModelsPlaceholder')
              : t('settings.modelNamePlaceholder')
          }
          className={INPUT_CLS}
        />
        {isOpen && (
          <div className="absolute z-50 mt-1 max-h-60 w-full overflow-hidden rounded border border-border bg-background-secondary shadow-lg">
            {isLoading && (
              <div className="px-3 py-2 text-xs text-text-tertiary">
                {t('settings.loadingModels')}
              </div>
            )}
            {error && (
              <div className="px-3 py-2 text-xs text-text-tertiary">
                {t('settings.errorLoadingModels')}
              </div>
            )}
            {!isLoading && !error && selectable.length === 0 && query.trim() !== '' && (
              <div className="px-3 py-2 text-xs text-text-tertiary">
                {t('settings.noModelsFound')}
              </div>
            )}
            {!isLoading && !error && (
              <div ref={listRef} className="max-h-52 overflow-y-auto">
                {selectable.map((m, i) => (
                  <button
                    type="button"
                    key={m.id}
                    className={`flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-xs ${
                      i === selectedIndex
                        ? 'bg-interaction-primary/20 text-text-primary'
                        : 'text-text-primary hover:bg-background-tertiary'
                    }`}
                    onClick={() => handleSelect(m)}
                    onMouseEnter={() => setSelectedIndex(i)}
                  >
                    <span className="min-w-0 flex-1 truncate font-mono">
                      {m.id}
                      {m.owned_by && (
                        <span className="ml-2 text-text-tertiary">({m.owned_by})</span>
                      )}
                    </span>
                    <span className="flex shrink-0 items-center gap-1 text-[10px] text-text-tertiary">
                      {typeof m.context_window === 'number' && m.context_window > 0 && (
                        <span className="rounded bg-background-tertiary px-1 py-0.5 font-mono">
                          {formatContextWindow(m.context_window)}
                        </span>
                      )}
                      {m.vision && (
                        <span className="rounded bg-background-tertiary px-1 py-0.5">vision</span>
                      )}
                      {m.thinking_levels?.length ? (
                        <span className="rounded bg-background-tertiary px-1 py-0.5 font-mono">
                          {m.thinking_levels.join('/')}
                        </span>
                      ) : (
                        m.reasoning && (
                          <span className="rounded bg-background-tertiary px-1 py-0.5">think</span>
                        )
                      )}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
      <AddButton onClick={handleAddManual} disabled={!query.trim()}>
        {t('common.add')}
      </AddButton>
    </div>
  )
}
