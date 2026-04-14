import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SecretMode, SecretValue } from '../../lib/types'

type Props = {
  id: string
  value: SecretValue
  onChange: (value: SecretValue) => void
  placeholder?: string
  disabled?: boolean
}

export function SecretInput({ id, value, onChange, placeholder, disabled }: Props) {
  const { t } = useTranslation()
  const [showValue, setShowValue] = useState(false)
  const titleText = showValue ? t('settings.hideSecret') : t('settings.showSecret')
  const [mode, setMode] = useState<SecretMode>(value.mode || 'empty')
  const [literalValue, setLiteralValue] = useState(value.value || '')
  const [envName, setEnvName] = useState(value.env_name || '')

  useEffect(() => {
    setMode(value.mode || 'empty')
    setLiteralValue(value.value || '')
    setEnvName(value.env_name || '')
  }, [value.mode, value.value, value.env_name])

  const handleModeChange = (newMode: SecretMode) => {
    setMode(newMode)
    onChange({
      mode: newMode,
      value: newMode === 'literal' ? literalValue : undefined,
      env_name: newMode === 'env' ? envName : undefined,
      has_env_var: newMode === 'env' && !!envName,
    })
  }

  const handleLiteralChange = (newValue: string) => {
    setLiteralValue(newValue)
    if (mode === 'literal') {
      onChange({
        mode: 'literal',
        value: newValue,
        has_env_var: false,
      })
    }
  }

  const handleEnvChange = (newEnvName: string) => {
    setEnvName(newEnvName)
    if (mode === 'env') {
      onChange({
        mode: 'env',
        env_name: newEnvName,
        has_env_var: !!newEnvName,
      })
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => handleModeChange('literal')}
          disabled={disabled}
          className={`rounded px-2 py-1 text-[11px] ${
            mode === 'literal'
              ? 'bg-blue-600 text-white'
              : 'bg-[#2a2a2a] text-[#888] hover:bg-[#333]'
          }`}
        >
          {t('settings.literalValue')}
        </button>
        <button
          type="button"
          onClick={() => handleModeChange('env')}
          disabled={disabled}
          className={`rounded px-2 py-1 text-[11px] ${
            mode === 'env' ? 'bg-blue-600 text-white' : 'bg-[#2a2a2a] text-[#888] hover:bg-[#333]'
          }`}
        >
          {t('settings.envVariable')}
        </button>
        <button
          type="button"
          onClick={() => handleModeChange('empty')}
          disabled={disabled}
          className={`rounded px-2 py-1 text-[11px] ${
            mode === 'empty' ? 'bg-blue-600 text-white' : 'bg-[#2a2a2a] text-[#888] hover:bg-[#333]'
          }`}
        >
          {t('settings.empty')}
        </button>
      </div>

      {mode === 'literal' && (
        <div className="relative">
          <input
            id={id}
            type={showValue ? 'text' : 'password'}
            value={literalValue}
            onChange={(e) => handleLiteralChange(e.target.value)}
            disabled={disabled}
            placeholder={placeholder}
            className="w-full rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 pr-10 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none"
          />
          <button
            type="button"
            onClick={() => setShowValue(!showValue)}
            title={titleText}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-[#666] hover:text-[#aaa]"
          >
            {showValue ? (
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <title>{titleText}</title>
                <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
                <line x1="1" y1="1" x2="23" y2="23" />
              </svg>
            ) : (
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <title>{titleText}</title>
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                <circle cx="12" cy="12" r="3" />
              </svg>
            )}
          </button>
        </div>
      )}

      {mode === 'env' && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-[#666]">$</span>
          <input
            id={id}
            type="text"
            value={envName}
            onChange={(e) => handleEnvChange(e.target.value)}
            disabled={disabled}
            placeholder="ENV_VAR_NAME"
            className="flex-1 rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none font-mono"
          />
        </div>
      )}

      {mode === 'empty' && (
        <p className="text-xs text-[#666] italic">{t('settings.emptySecretDescription')}</p>
      )}
    </div>
  )
}
