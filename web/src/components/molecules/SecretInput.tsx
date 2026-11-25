import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { SecretMode, SecretValue } from '../../lib/types'

type Props = {
  id: string
  value: SecretValue
  onChange: (value: SecretValue) => void
  placeholder?: string
  disabled?: boolean
  autoFocus?: boolean
}

const MODE_BUTTON_IDLE =
  'rounded px-2 py-1 text-xs bg-background-tertiary text-text-secondary hover:bg-surface-hover'
const MODE_BUTTON_ACTIVE = 'rounded px-2 py-1 text-xs bg-accent-primary text-text-on-accent'

export function SecretInput({
  id,
  value,
  onChange,
  placeholder,
  disabled,
  autoFocus = false,
}: Props) {
  const { t } = useTranslation()
  const [showValue, setShowValue] = useState(false)
  const [replacing, setReplacing] = useState(false)
  const [mode, setMode] = useState<SecretMode>(value.mode || 'empty')
  const [literalValue, setLiteralValue] = useState(value.value || '')
  const [envName, setEnvName] = useState(value.env_name || '')
  const [secretName, setSecretName] = useState(value.secret_name || '')

  useEffect(() => {
    setMode(value.mode || 'empty')
    setLiteralValue(value.value || '')
    setEnvName(value.env_name || '')
    setSecretName(value.secret_name || '')
  }, [value.mode, value.value, value.env_name, value.secret_name])

  const hasStoredLiteral = !!value.has_value && !value.value
  const showStoredLiteral = mode === 'literal' && hasStoredLiteral && !replacing
  const titleText = showValue ? t('settings.hideSecret') : t('settings.showSecret')

  useEffect(() => {
    if (!hasStoredLiteral) setReplacing(false)
  }, [hasStoredLiteral])

  const literalPayload = (nextValue: string): SecretValue => ({
    mode: 'literal',
    value: nextValue || undefined,
    env_name: undefined,
    secret_name: undefined,
    has_env_var: false,
    has_value: nextValue === '' && hasStoredLiteral,
  })

  const handleModeChange = (newMode: SecretMode) => {
    setMode(newMode)
    setReplacing(false)
    if (newMode === 'literal') {
      onChange(literalPayload(literalValue))
      return
    }
    if (newMode === 'env') {
      onChange({
        mode: 'env',
        env_name: envName || undefined,
        has_env_var: !!envName,
        has_value: false,
      })
      return
    }
    if (newMode === 'keyring') {
      onChange({
        mode: 'keyring',
        secret_name: secretName || undefined,
        has_env_var: false,
        has_value: false,
      })
      return
    }
    onChange({ mode: 'empty', has_env_var: false, has_value: false })
  }

  const handleLiteralChange = (newValue: string) => {
    setLiteralValue(newValue)
    if (mode === 'literal') onChange(literalPayload(newValue))
  }

  const handleEnvChange = (newEnvName: string) => {
    setEnvName(newEnvName)
    if (mode === 'env') {
      onChange({
        mode: 'env',
        env_name: newEnvName || undefined,
        has_env_var: !!newEnvName,
        has_value: false,
      })
    }
  }

  const handleSecretNameChange = (newSecretName: string) => {
    setSecretName(newSecretName)
    if (mode === 'keyring') {
      onChange({
        mode: 'keyring',
        secret_name: newSecretName || undefined,
        has_env_var: false,
        has_value: false,
      })
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => handleModeChange('literal')}
          disabled={disabled}
          className={`rounded-md px-3 py-1.5 text-xs transition-all ${
            mode === 'literal'
              ? 'bg-accent-primary text-text-on-accent shadow-sm'
              : 'bg-background-tertiary text-text-secondary hover:bg-surface-hover hover:text-text-primary'
          }`}
        >
          {t('settings.literalValue')}
        </button>
        <button
          type="button"
          onClick={() => handleModeChange('env')}
          disabled={disabled}
          className={mode === 'env' ? MODE_BUTTON_ACTIVE : MODE_BUTTON_IDLE}
        >
          {t('settings.envVariable')}
        </button>
        <button
          type="button"
          onClick={() => handleModeChange('keyring')}
          disabled={disabled}
          className={mode === 'keyring' ? MODE_BUTTON_ACTIVE : MODE_BUTTON_IDLE}
        >
          {t('settings.keyringSecret')}
        </button>
        <button
          type="button"
          onClick={() => handleModeChange('empty')}
          disabled={disabled}
          className={mode === 'empty' ? MODE_BUTTON_ACTIVE : MODE_BUTTON_IDLE}
        >
          {t('settings.empty')}
        </button>
      </div>

      {showStoredLiteral && (
        <div className="flex items-center gap-2">
          <span className="flex-1 rounded border border-border bg-background-tertiary px-3 py-2 text-xs text-text-tertiary">
            {t('settings.storedSecret')}
          </span>
          <button
            type="button"
            onClick={() => setReplacing(true)}
            disabled={disabled}
            className="rounded bg-background-tertiary px-3 py-1.5 text-xs text-text-secondary hover:bg-surface-hover hover:text-text-primary"
          >
            {t('settings.replaceSecret')}
          </button>
        </div>
      )}

      {mode === 'literal' && !showStoredLiteral && (
        <div className="relative">
          <input
            id={id}
            type={showValue ? 'text' : 'password'}
            value={literalValue}
            onChange={(e) => handleLiteralChange(e.target.value)}
            disabled={disabled}
            placeholder={placeholder}
            // biome-ignore lint/a11y/noAutofocus: opt-in (default false) so a parent can move the caret here right after switching to literal mode — the only mode where this input exists
            autoFocus={autoFocus}
            className="w-full rounded border border-border bg-background-primary px-3 py-2 pr-10 text-xs text-text-primary placeholder:text-text-tertiary focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus focus:ring-offset-2 focus:ring-offset-background-primary"
          />
          <button
            type="button"
            onClick={() => setShowValue(!showValue)}
            title={titleText}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-text-tertiary hover:text-text-secondary"
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
          <span className="text-xs text-text-tertiary">$</span>
          <input
            id={id}
            type="text"
            value={envName}
            onChange={(e) => handleEnvChange(e.target.value)}
            disabled={disabled}
            placeholder="ENV_VAR_NAME"
            className="flex-1 rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus focus:ring-offset-2 focus:ring-offset-background-primary font-mono"
          />
        </div>
      )}

      {mode === 'keyring' && (
        <div className="flex items-center gap-2">
          <input
            id={id}
            type="text"
            value={secretName}
            onChange={(e) => handleSecretNameChange(e.target.value)}
            disabled={disabled}
            placeholder="secret.name"
            className="flex-1 rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-focus focus:outline-none focus:ring-2 focus:ring-focus focus:ring-offset-2 focus:ring-offset-background-primary font-mono"
          />
        </div>
      )}

      {mode === 'empty' && (
        <p className="text-xs text-text-tertiary italic">{t('settings.emptySecretDescription')}</p>
      )}
    </div>
  )
}
