import { useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import type { EditableNamedProviderConfig, SecretValue } from '../../../lib/types'
import { Modal } from '../../atoms'
import { BooleanInput, SecretInput, TextInput } from '../../molecules'

const PROVIDER_TYPES = [
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
  { value: 'claude-code', label: 'Claude Code (CLI)' },
  { value: 'codex-cli', label: 'Codex CLI' },
  { value: 'custom', label: 'OpenAI Compatible' },
]

const DEFAULT_API_BASE: Record<string, string> = {
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
  zai_coding_plan: 'https://api.z.ai/api/paas/v4',
  modelark_coding_plan: 'https://ark.ap-southeast.bytepluses.com/api/coding/v3',
  github_copilot: 'localhost:4321',
  'claude-code': '',
  'codex-cli': '',
  custom: '',
}

type Props = {
  isOpen: boolean
  onClose: () => void
}

export function AddProviderModal({ isOpen, onClose }: Props) {
  const { draftConfig, updateField, t } = useSettings()
  const [step, setStep] = useState(1)
  const [providerType, setProviderType] = useState('')
  const [customType, setCustomType] = useState('')
  const [providerName, setProviderName] = useState('')
  const [apiKeyMode, setApiKeyMode] = useState<'literal' | 'env' | 'empty'>('empty')
  const [apiKeyValue, setApiKeyValue] = useState('')
  const [apiKeyEnvName, setApiKeyEnvName] = useState('')
  const [apiBase, setApiBase] = useState('')
  const [proxy, setProxy] = useState('')
  const [webSearch, setWebSearch] = useState(false)

  const effectiveType = providerType === 'custom' ? customType : providerType

  const handleTypeChange = (type: string) => {
    setProviderType(type)
    setApiBase(DEFAULT_API_BASE[type] || '')
  }

  const resetForm = () => {
    setStep(1)
    setProviderType('')
    setCustomType('')
    setProviderName('')
    setApiKeyMode('empty')
    setApiKeyValue('')
    setApiKeyEnvName('')
    setApiBase('')
    setProxy('')
    setWebSearch(false)
  }

  const handleClose = () => {
    resetForm()
    onClose()
  }

  const handleAdd = () => {
    if (!draftConfig || !effectiveType || !providerName.trim()) return

    const providers = draftConfig.providers || {}
    const secretValue: SecretValue = {
      mode: apiKeyMode,
      value: apiKeyMode === 'literal' ? apiKeyValue : undefined,
      env_name: apiKeyMode === 'env' ? apiKeyEnvName : undefined,
      has_env_var: apiKeyMode === 'env' && !!apiKeyEnvName,
    }

    const newProvider: EditableNamedProviderConfig = {
      type: effectiveType,
      api_key: secretValue,
      api_base: apiBase,
      proxy: proxy || undefined,
      web_search: webSearch,
    }

    updateField('providers', {
      ...providers,
      [providerName.trim()]: newProvider,
    })

    handleClose()
  }

  const canProceedStep1 =
    providerType !== '' && (providerType !== 'custom' || customType.trim() !== '')
  const canProceedStep2 = providerName.trim() !== ''
  const canAdd = canProceedStep1 && canProceedStep2

  const SELECT_CLS =
    'w-full rounded-lg border border-border bg-background-tertiary px-3.5 py-2.5 text-xs text-text-primary focus:border-interaction-primary focus:outline-none transition-all duration-200 focus:ring-2 focus:ring-interaction-primary/20'
  const INPUT_CLS =
    'w-full rounded-lg border border-border bg-background-tertiary px-3.5 py-2.5 text-xs text-text-primary placeholder-text-tertiary focus:border-interaction-primary focus:outline-none transition-all duration-200 focus:ring-2 focus:ring-interaction-primary/20'
  const BTN_CLS =
    'rounded-lg px-4 py-2.5 text-xs font-medium transition-all duration-200 active:scale-[0.98] disabled:opacity-40 disabled:cursor-not-allowed'
  const BTN_PRIMARY = `${BTN_CLS} bg-cta-primary text-text-on-accent hover:bg-cta-hover`
  const BTN_SECONDARY = `${BTN_CLS} bg-surface-secondary text-text-secondary hover:bg-surface-hover`

  return (
    <Modal isOpen={isOpen} onClose={handleClose} title={t('settings.addProviderModal.title')}>
      <div className="p-6">
        <div className="mb-4 flex gap-2">
          {[1, 2, 3].map((s) => (
            <div
              key={s}
              className={`flex-1 h-1 rounded ${step >= s ? 'bg-interaction-primary' : 'bg-surface-tertiary'}`}
            />
          ))}
        </div>

        {step === 1 && (
          <div className="space-y-4 animate-step-enter">
            <p className="text-xs text-text-secondary">{t('settings.addProviderModal.stepType')}</p>
            <select
              value={providerType}
              onChange={(e) => handleTypeChange(e.target.value)}
              className={SELECT_CLS}
            >
              <option value="" disabled>
                {t('settings.addProviderModal.selectType')}
              </option>
              {PROVIDER_TYPES.map((pt) => (
                <option key={pt.value} value={pt.value}>
                  {pt.label}
                </option>
              ))}
            </select>
            {providerType === 'custom' && (
              <input
                type="text"
                value={customType}
                onChange={(e) => setCustomType(e.target.value)}
                placeholder={t('settings.addProviderModal.customTypePlaceholder')}
                className={INPUT_CLS}
              />
            )}
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4 animate-step-enter">
            <p className="text-xs text-text-secondary">{t('settings.addProviderModal.stepName')}</p>
            <input
              type="text"
              value={providerName}
              onChange={(e) => setProviderName(e.target.value)}
              placeholder={t('settings.addProviderModal.namePlaceholder')}
              className={INPUT_CLS}
            />
            <p className="text-xs text-text-tertiary">{t('settings.addProviderModal.nameHint')}</p>
          </div>
        )}

        {step === 3 && (
          <div className="space-y-4 animate-step-enter">
            <p className="text-xs text-text-secondary">
              {t('settings.addProviderModal.stepConfig')}
            </p>
            <div className="space-y-3">
              <div>
                <label
                  htmlFor="new-provider-api-key"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerApiKey')}
                </label>
                <SecretInput
                  id="new-provider-api-key"
                  value={{
                    mode: apiKeyMode,
                    value: apiKeyValue,
                    env_name: apiKeyEnvName,
                    has_env_var: apiKeyMode === 'env' && !!apiKeyEnvName,
                  }}
                  onChange={(v: SecretValue) => {
                    setApiKeyMode(v.mode)
                    if (v.mode === 'literal') setApiKeyValue(v.value || '')
                    if (v.mode === 'env') setApiKeyEnvName(v.env_name || '')
                  }}
                />
              </div>
              <div>
                <label
                  htmlFor="new-provider-api-base"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerApiBase')}
                </label>
                <TextInput id="new-provider-api-base" value={apiBase} onChange={setApiBase} />
              </div>
              <div>
                <label
                  htmlFor="new-provider-proxy"
                  className="mb-1 block text-xs text-text-secondary"
                >
                  {t('settings.fields.providerProxy')}
                </label>
                <TextInput id="new-provider-proxy" value={proxy} onChange={setProxy} />
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
          </div>
        )}

        <div className="mt-6 flex justify-between">
          <button
            type="button"
            onClick={() => setStep(step - 1)}
            disabled={step === 1}
            className={BTN_SECONDARY}
          >
            {t('settings.addProviderModal.back')}
          </button>
          {step < 3 ? (
            <button
              type="button"
              onClick={() => setStep(step + 1)}
              disabled={step === 1 ? !canProceedStep1 : !canProceedStep2}
              className={BTN_PRIMARY}
            >
              {t('settings.addProviderModal.next')}
            </button>
          ) : (
            <button type="button" onClick={handleAdd} disabled={!canAdd} className={BTN_PRIMARY}>
              {t('settings.addProviderModal.add')}
            </button>
          )}
        </div>
      </div>
    </Modal>
  )
}
