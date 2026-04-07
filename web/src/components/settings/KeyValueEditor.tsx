import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { StringListEditor } from './StringListEditor'

type Props = {
  id: string
  value: Record<string, string[]>
  onChange: (value: Record<string, string[]>) => void
  disabled?: boolean
  keyPlaceholder?: string
}

export function KeyValueEditor({ id, value, onChange, disabled, keyPlaceholder }: Props) {
  const { t } = useTranslation()
  const removeTitle = t('common.remove')
  const [newKey, setNewKey] = useState('')

  const addEntry = () => {
    const key = newKey.trim()
    if (key && !(key in value)) {
      onChange({ ...value, [key]: [] })
      setNewKey('')
    }
  }

  const removeEntry = (key: string) => {
    const updated = { ...value }
    delete updated[key]
    onChange(updated)
  }

  const updateValue = (key: string, values: string[]) => {
    onChange({ ...value, [key]: values })
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      addEntry()
    }
  }

  const entries = Object.entries(value || {})

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <input
          type="text"
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          onKeyDown={handleKeyDown}
          disabled={disabled}
          placeholder={keyPlaceholder}
          className="flex-1 rounded border border-[#3a3a3a] bg-[#1a1a1a] px-3 py-2 text-xs text-[#e0e0e0] placeholder-[#555] focus:border-blue-500 focus:outline-none disabled:opacity-50"
        />
        <button
          type="button"
          onClick={addEntry}
          disabled={disabled || !newKey.trim()}
          className="rounded bg-blue-600 px-3 py-2 text-xs text-white transition-colors hover:bg-blue-500 disabled:opacity-50"
        >
          {t('common.add')}
        </button>
      </div>
      {entries.map(([key, values]) => (
        <div key={key} className="rounded border border-[#2e2e2e] bg-[#1a1a1a] p-3">
          <div className="mb-2 flex items-center justify-between">
            <span className="font-mono text-xs font-medium text-white">{key}</span>
            <button
              type="button"
              onClick={() => removeEntry(key)}
              disabled={disabled}
              title={removeTitle}
              aria-label={removeTitle}
              className="text-rose-400 transition-colors hover:text-rose-300 disabled:opacity-50"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
                <path d="M18 6L6 18M6 6l12 12" />
              </svg>
            </button>
          </div>
          <StringListEditor
            id={`${id}.${key}`}
            value={values}
            onChange={(v) => updateValue(key, v)}
            disabled={disabled}
          />
        </div>
      ))}
    </div>
  )
}
