import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import { detectProviderFromKey } from '../../../lib/keyPrefix'
import type { CatalogProvider, EditableNamedProviderConfig, SecretValue } from '../../../lib/types'
import { getProviderDisplayName } from '../../../lib/utils'
import { Modal } from '../../atoms'
import { CheckIcon, ChevronRightIcon } from '../../atoms/Icons'
import { BooleanInput, SecretInput } from '../../molecules'

export const PROVIDER_TYPES = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'groq', label: 'Groq' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'ollama', label: 'Ollama' },
  { value: 'vllm', label: 'vLLM' },
  { value: 'nvidia', label: 'NVIDIA' },
  { value: 'moonshot', label: 'Moonshot' },
  { value: 'zhipu', label: 'Zhipu' },
  { value: 'nanogpt', label: 'NanoGPT' },
  { value: 'chutes', label: 'Chutes' },
  { value: 'alibaba', label: 'Alibaba' },
  { value: 'alibaba_coding_plan', label: 'Alibaba Coding Plan' },
  { value: 'shengsuanyun', label: 'ShengSuanYun' },
  { value: 'zai_coding_plan', label: 'zAI Coding Plan' },
  { value: 'modelark_coding_plan', label: 'ModelArk Coding Plan' },
  { value: 'github_copilot', label: 'GitHub Copilot' },
  { value: 'opencode', label: 'OpenCode Zen' },
  { value: 'opencode_go', label: 'OpenCode Go' },
  { value: 'claude-code', label: 'Claude Code (CLI)' },
  { value: 'codex-cli', label: 'Codex CLI' },
  { value: 'custom', label: 'OpenAI Compatible' },
]

export const DEFAULT_API_BASE: Record<string, string> = {
  anthropic: 'https://api.anthropic.com/v1',
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  groq: 'https://api.groq.com/openai/v1',
  gemini: 'https://generativelanguage.googleapis.com/v1beta',
  deepseek: 'https://api.deepseek.com/v1',
  ollama: 'http://localhost:11434/v1',
  vllm: '',
  nvidia: 'https://integrate.api.nvidia.com/v1',
  moonshot: 'https://api.moonshot.cn/v1',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4',
  nanogpt: 'https://nano-gpt.com/api/v1',
  chutes: 'https://llm.chutes.ai/v1',
  alibaba: 'https://coding-intl.dashscope.aliyuncs.com/v1',
  alibaba_coding_plan: 'https://coding-intl.dashscope.aliyuncs.com/v1',
  shengsuanyun: 'https://router.shengsuanyun.com/api/v1',
  zai: 'https://api.z.ai/api/paas/v4',
  zai_coding_plan: 'https://api.z.ai/api/coding/paas/v4',
  modelark_coding_plan: 'https://ark.ap-southeast.bytepluses.com/api/coding/v3',
  github_copilot: 'localhost:4321',
  opencode: 'https://opencode.ai/zen/v1',
  opencode_go: 'https://opencode.ai/zen/go/v1',
  'claude-code': '',
  'codex-cli': '',
  custom: '',
}

const INITIAL_API_KEY: SecretValue = { mode: 'literal', value: '', has_env_var: false }

type ProviderTile = {
  type: string
  label: string
  apiBase: string
  modelCount?: number
  source: 'static' | 'catalog' | 'both' | 'detected'
}

type Props = {
  isOpen: boolean
  onClose: () => void
}

function toSlug(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function dedupeName(base: string, existingNames: string[]): string {
  const taken = new Set(existingNames)
  let name = base
  let suffix = 2
  while (taken.has(name)) {
    name = `${base}-${suffix}`
    suffix += 1
  }
  return name
}

function sanitizeProviderName(raw: string): string {
  return raw.replace(/[^A-Za-z0-9_-]/g, '').trim()
}

export function uniqueProviderName(type: string, label: string, existingNames: string[]): string {
  const slug = toSlug(type) || toSlug(label) || 'provider'
  return dedupeName(slug, existingNames)
}

export function AddProviderModal({ isOpen, onClose }: Props) {
  const { api, draftConfig, updateField, t } = useSettings()

  const [query, setQuery] = useState('')
  const [pickedType, setPickedType] = useState('')
  const [userPicked, setUserPicked] = useState(false)
  const [customType, setCustomType] = useState('')
  const [apiKey, setApiKey] = useState<SecretValue>(INITIAL_API_KEY)
  const [nameDraft, setNameDraft] = useState('')
  const [apiBase, setApiBase] = useState('')
  const [apiBaseEdited, setApiBaseEdited] = useState(false)
  const [proxy, setProxy] = useState('')
  const [webSearch, setWebSearch] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [catalog, setCatalog] = useState<CatalogProvider[]>([])
  const [keyFieldMount, setKeyFieldMount] = useState(0)

  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!isOpen) return
    let cancelled = false
    api
      .catalogProviders()
      .then((res) => {
        if (!cancelled) setCatalog(res.providers ?? [])
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [isOpen, api])

  const resetForm = useCallback(() => {
    setQuery('')
    setPickedType('')
    setUserPicked(false)
    setCustomType('')
    setApiKey(INITIAL_API_KEY)
    setNameDraft('')
    setApiBase('')
    setApiBaseEdited(false)
    setProxy('')
    setWebSearch(false)
    setAdvancedOpen(false)
    setKeyFieldMount(0)
  }, [])

  useEffect(() => {
    if (!isOpen) return
    resetForm()
    searchRef.current?.focus()
  }, [isOpen, resetForm])

  const selectedType = userPicked ? pickedType : detectProviderFromKey(apiKey.value ?? '') || ''
  const effectiveType = selectedType === 'custom' ? customType.trim() : selectedType

  const tiles = useMemo<ProviderTile[]>(() => {
    const byType = new Map<string, ProviderTile>()
    for (const pt of PROVIDER_TYPES) {
      byType.set(pt.value, {
        type: pt.value,
        label: pt.label,
        apiBase: DEFAULT_API_BASE[pt.value] ?? '',
        source: 'static',
      })
    }
    for (const provider of catalog) {
      if (!provider.id) continue
      const current = byType.get(provider.id)
      byType.set(provider.id, {
        type: provider.id,
        label: current?.label || provider.name || getProviderDisplayName(provider.id),
        apiBase: DEFAULT_API_BASE[provider.id] || provider.api_base || '',
        modelCount:
          provider.model_count && provider.model_count > 0
            ? provider.model_count
            : current?.modelCount,
        source: current ? 'both' : 'catalog',
      })
    }
    if (selectedType !== '' && !byType.has(selectedType)) {
      byType.set(selectedType, {
        type: selectedType,
        label: getProviderDisplayName(selectedType),
        apiBase: DEFAULT_API_BASE[selectedType] ?? '',
        source: 'detected',
      })
    }
    const curated = [...byType.values()].filter((tile) => tile.source !== 'catalog')
    const discovered = [...byType.values()]
      .filter((tile) => tile.source === 'catalog')
      .sort((a, b) => a.label.localeCompare(b.label))
    return [...curated, ...discovered]
  }, [catalog, selectedType])

  const filteredTiles = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return tiles
    return tiles.filter(
      (tile) => tile.label.toLowerCase().includes(q) || tile.type.toLowerCase().includes(q),
    )
  }, [tiles, query])

  const selectedTile = tiles.find((tile) => tile.type === selectedType)
  const canAdd =
    !!draftConfig &&
    effectiveType !== '' &&
    filteredTiles.some((tile) => tile.type === selectedType)

  const providersDraft = draftConfig?.providers ?? {}
  const tileLabel =
    selectedType === 'custom' ? effectiveType : (selectedTile?.label ?? effectiveType)
  const derivedName = uniqueProviderName(effectiveType, tileLabel, Object.keys(providersDraft))

  useEffect(() => {
    if (apiBaseEdited) return
    setApiBase(selectedTile?.apiBase ?? '')
  }, [selectedTile, apiBaseEdited])

  const handleSelectTile = (tile: ProviderTile) => {
    setUserPicked(true)
    setPickedType(tile.type)
    setKeyFieldMount((n) => n + 1)
  }

  const handleSecretChange = (next: SecretValue) => {
    if (next.mode !== apiKey.mode && selectedType !== '') {
      setUserPicked(true)
      setPickedType(selectedType)
    }
    setApiKey(next)
  }

  const handleClose = () => {
    resetForm()
    onClose()
  }

  const handleAdd = () => {
    if (!canAdd || !draftConfig) return

    const secret: SecretValue =
      apiKey.mode === 'literal' && (apiKey.value ?? '').trim() !== ''
        ? { mode: 'literal', value: apiKey.value, has_env_var: false }
        : apiKey.mode === 'env' && (apiKey.env_name ?? '').trim() !== ''
          ? { mode: 'env', env_name: apiKey.env_name, has_env_var: true }
          : { mode: 'empty', has_env_var: false }

    const draftName = sanitizeProviderName(nameDraft)
    const name = draftName ? dedupeName(draftName, Object.keys(providersDraft)) : derivedName

    const newProvider: EditableNamedProviderConfig = {
      type: effectiveType,
      api_key: secret,
      api_base: apiBase,
      proxy: proxy || undefined,
      web_search: webSearch,
    }

    updateField('providers', {
      ...providersDraft,
      [name]: newProvider,
    })

    handleClose()
  }

  const INPUT_CLS =
    'w-full rounded-lg border border-border bg-background-tertiary px-3.5 py-2.5 text-xs text-text-primary placeholder-text-tertiary focus:border-focus focus:outline-none transition-all duration-fast focus:ring-2 focus:ring-focus/20'
  const BTN_CLS =
    'rounded-lg px-4 py-2.5 text-xs font-medium transition-all duration-fast active:scale-[0.98] disabled:opacity-40 disabled:cursor-not-allowed'
  const BTN_PRIMARY = `${BTN_CLS} bg-accent-primary text-text-on-accent hover:bg-accent-hover`
  const BTN_SECONDARY = `${BTN_CLS} bg-background-tertiary text-text-secondary hover:bg-surface-hover`

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClose}
      title={t('settings.addProviderModal.title')}
      size="lg"
    >
      <div className="space-y-4 p-6">
        <div className="-mx-6 -mt-6 mb-2 border-b border-border bg-background-primary px-6 py-3">
          <input
            ref={searchRef}
            type="text"
            aria-label={t('settings.addProviderModal.searchPlaceholder')}
            placeholder={t('settings.addProviderModal.searchPlaceholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className={INPUT_CLS}
          />
        </div>

        {/* biome-ignore lint/a11y/useSemanticElements: a tile chooser is not a form field — <fieldset> would add form semantics, a legend and UA borders to a modal that never submits */}
        <div
          role="group"
          aria-label={t('settings.fields.providerType')}
          className={`grid grid-cols-2 gap-2 overflow-y-auto pr-1 ${advancedOpen ? 'max-h-[24vh]' : 'max-h-[38vh]'}`}
        >
          {filteredTiles.map((tile) => {
            const selected = tile.type === selectedType
            return (
              <button
                key={tile.type}
                type="button"
                aria-pressed={selected}
                data-provider-type={tile.type}
                onClick={() => handleSelectTile(tile)}
                className={`flex items-start justify-between gap-2 rounded-lg border p-3 text-left transition-all duration-fast ${
                  selected
                    ? 'border-focus ring-2 ring-focus/20 bg-focus/10'
                    : 'border-border bg-background-tertiary hover:bg-surface-hover'
                }`}
              >
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium text-text-primary">
                    {tile.label}
                  </span>
                  <span className="block truncate text-2xs text-text-tertiary">
                    {tile.type}
                    {tile.modelCount
                      ? ` · ${t('settings.addProviderModal.modelsCount', { count: tile.modelCount })}`
                      : ''}
                  </span>
                </span>
                {selected && (
                  <span className="flex-none text-state-info">
                    <CheckIcon size={14} />
                  </span>
                )}
              </button>
            )
          })}
        </div>

        {filteredTiles.length === 0 && (
          <p className="py-6 text-center text-xs text-text-tertiary">
            {t('settings.addProviderModal.noResults')}
          </p>
        )}

        {selectedType === 'custom' && (
          <div>
            <label
              htmlFor="new-provider-custom-type"
              className="mb-1 block text-xs text-text-secondary"
            >
              {t('settings.addProviderModal.customTypeLabel')}
            </label>
            <input
              id="new-provider-custom-type"
              type="text"
              value={customType}
              onChange={(e) => setCustomType(e.target.value)}
              placeholder={t('settings.addProviderModal.customTypePlaceholder')}
              className={INPUT_CLS}
            />
          </div>
        )}

        <div>
          <label htmlFor="new-provider-api-key" className="mb-1 block text-xs text-text-secondary">
            {t('settings.fields.providerApiKey')}
          </label>
          <SecretInput
            key={keyFieldMount}
            id="new-provider-api-key"
            autoFocus={keyFieldMount > 0}
            value={apiKey}
            onChange={handleSecretChange}
            placeholder="sk-..."
          />
          <div id="new-provider-api-key-hint" className="mt-1 flex flex-wrap items-center gap-2">
            <p className="text-xs text-text-tertiary">
              {t('settings.addProviderModal.apiKeyHint')}
            </p>
            {selectedType !== '' && (
              <span
                data-detected-provider={selectedType}
                className="rounded-full bg-state-info/10 px-1.5 py-0.5 text-2xs font-medium text-state-info"
              >
                {selectedTile?.label ?? getProviderDisplayName(effectiveType)}
              </span>
            )}
          </div>
        </div>

        <div>
          <button
            type="button"
            aria-expanded={advancedOpen}
            aria-controls="new-provider-advanced"
            onClick={() => setAdvancedOpen(!advancedOpen)}
            className="flex items-center gap-1.5 text-xs font-medium text-text-secondary hover:text-text-primary"
          >
            <ChevronRightIcon
              size={12}
              className={`transition-transform duration-fast ${advancedOpen ? 'rotate-90' : ''}`}
            />
            {t('settings.addProviderModal.advanced')}
          </button>

          {advancedOpen && (
            <div id="new-provider-advanced" className="mt-3 space-y-3 animate-step-enter">
              <div>
                <label
                  htmlFor="new-provider-name"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.addProviderModal.nameLabel')}
                </label>
                <input
                  id="new-provider-name"
                  type="text"
                  value={nameDraft}
                  onChange={(e) => setNameDraft(e.target.value)}
                  placeholder={derivedName}
                  className={INPUT_CLS}
                />
                <p className="mt-1 text-xs text-text-tertiary">
                  {t('settings.addProviderModal.nameHint')}
                </p>
              </div>

              <div>
                <label
                  htmlFor="new-provider-api-base"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerApiBase')}
                </label>
                <input
                  id="new-provider-api-base"
                  type="text"
                  value={apiBase}
                  onChange={(e) => {
                    setApiBaseEdited(true)
                    setApiBase(e.target.value)
                  }}
                  className={INPUT_CLS}
                />
              </div>

              <div>
                <label
                  htmlFor="new-provider-proxy"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerProxy')}
                </label>
                <input
                  id="new-provider-proxy"
                  type="text"
                  value={proxy}
                  onChange={(e) => setProxy(e.target.value)}
                  className={INPUT_CLS}
                />
              </div>

              <div>
                <label
                  htmlFor="new-provider-web-search"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerWebSearch')}
                </label>
                <BooleanInput
                  id="new-provider-web-search"
                  value={webSearch}
                  onChange={setWebSearch}
                />
              </div>
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={handleClose} className={BTN_SECONDARY}>
            {t('common.cancel')}
          </button>
          <button type="button" onClick={handleAdd} disabled={!canAdd} className={BTN_PRIMARY}>
            {t('settings.addProviderModal.add')}
          </button>
        </div>
      </div>
    </Modal>
  )
}
